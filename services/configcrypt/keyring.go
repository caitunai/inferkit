// Package configcrypt provides versioned envelope encryption for configuration
// center payloads and keeps key-encryption keys outside the application data.
package configcrypt

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
)

const (
	// ProviderFile identifies the "file" value.
	ProviderFile   = "file"
	keyBytes       = 32
	keyringVersion = 1
	maxKeyringSize = 64 * 1024
	osWindows      = "windows"
)

var (
	// ErrDisabled indicates configuration encryption is disabled.
	ErrDisabled = errors.New("configuration encryption is disabled")
	// ErrInvalidSettings indicates invalid configuration encryption settings.
	ErrInvalidSettings = errors.New("invalid configuration encryption settings")
	// ErrInvalidKeyring indicates invalid configuration encryption keyring.
	ErrInvalidKeyring = errors.New("invalid configuration encryption keyring")
	// ErrInsecureKeyring indicates configuration encryption keyring permissions are insecure.
	ErrInsecureKeyring = errors.New("configuration encryption keyring permissions are insecure")
	// ErrKeyNotFound indicates configuration encryption key not found.
	ErrKeyNotFound = errors.New("configuration encryption key not found")
	// ErrEncrypt indicates configuration encryption failed.
	ErrEncrypt = errors.New("configuration encryption failed")
	// ErrDecrypt indicates configuration decryption failed.
	ErrDecrypt = errors.New("configuration decryption failed")
	// ErrCipherSetup indicates configuration cipher setup failed.
	ErrCipherSetup = errors.New("configuration cipher setup failed")
	// ErrKeyAlreadyExists indicates configuration encryption key already exists.
	ErrKeyAlreadyExists = errors.New("configuration encryption key already exists")
	// ErrKeyringWrite indicates configuration encryption keyring write failed.
	ErrKeyringWrite = errors.New("configuration encryption keyring write failed")
	keyIDPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// Settings represents settings data.
type Settings struct {
	Provider    string
	ActiveKeyID string
	KeyringPath string
	Enabled     bool
}

type keyringDocument struct {
	Keys    map[string]string `json:"keys"`
	Version int               `json:"version"`
}

func loadFileKeys(path string) (map[string][]byte, error) {
	document, err := readKeyring(path)
	if err != nil {
		return nil, err
	}
	keys := make(map[string][]byte, len(document.Keys))
	for id, encoded := range document.Keys {
		if !keyIDPattern.MatchString(id) {
			return nil, ErrInvalidKeyring
		}
		key, decodeErr := base64.StdEncoding.DecodeString(encoded)
		if decodeErr != nil || len(key) != keyBytes {
			return nil, ErrInvalidKeyring
		}
		keys[id] = key
	}
	return keys, nil
}

//nolint:cyclop,gocognit // This bounded keyring validation keeps every security check and failure classification explicit.
func readKeyring(path string) (*keyringDocument, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, ErrInvalidSettings
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, errors.Join(ErrInvalidKeyring, err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxKeyringSize {
		return nil, ErrInvalidKeyring
	}
	if runtime.GOOS != osWindows && info.Mode().Perm()&0o077 != 0 {
		return nil, ErrInsecureKeyring
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- the operator-configured absolute path is validated as a bounded regular file above.
	if err != nil {
		return nil, errors.Join(ErrInvalidKeyring, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document keyringDocument
	if documentErr := decoder.Decode(&document); documentErr != nil {
		return nil, errors.Join(ErrInvalidKeyring, documentErr)
	}
	var extra any
	if trailingErr := decoder.Decode(&extra); !errors.Is(trailingErr, io.EOF) {
		if trailingErr == nil {
			return nil, ErrInvalidKeyring
		}
		return nil, errors.Join(ErrInvalidKeyring, trailingErr)
	}
	if document.Version != keyringVersion || len(document.Keys) == 0 {
		return nil, ErrInvalidKeyring
	}
	return &document, nil
}

// GenerateFileKey creates a new AES-256 key and stores it in a separate
// keyring file. Existing keys are retained so older ciphertext stays readable.
//
//nolint:cyclop // This bounded keyring validation keeps every security check and failure classification explicit.
func GenerateFileKey(path, id string) error {
	if path == "" || !filepath.IsAbs(path) || !keyIDPattern.MatchString(id) {
		return ErrInvalidSettings
	}
	document := &keyringDocument{Version: keyringVersion, Keys: make(map[string]string)}
	if _, err := os.Stat(path); err == nil {
		existing, readErr := readKeyring(path)
		if readErr != nil {
			return readErr
		}
		document = existing
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.Join(ErrKeyringWrite, err)
	}
	if _, exists := document.Keys[id]; exists {
		return ErrKeyAlreadyExists
	}
	key := make([]byte, keyBytes)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return errors.Join(ErrKeyringWrite, err)
	}
	document.Keys[id] = base64.StdEncoding.EncodeToString(key)
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return errors.Join(ErrKeyringWrite, err)
	}
	raw = append(raw, '\n')
	if writeErr := writeKeyringAtomically(path, raw); writeErr != nil {
		return writeErr
	}
	return nil
}

func writeKeyringAtomically(path string, raw []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".config-center-keyring-*")
	if err != nil {
		return errors.Join(ErrKeyringWrite, err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if removeErr := os.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return
		}
	}()
	if chmodErr := temporary.Chmod(0o600); chmodErr != nil {
		return errors.Join(ErrKeyringWrite, chmodErr, temporary.Close())
	}
	if _, writeErr := temporary.Write(raw); writeErr != nil {
		return errors.Join(ErrKeyringWrite, writeErr, temporary.Close())
	}
	if syncErr := temporary.Sync(); syncErr != nil {
		return errors.Join(ErrKeyringWrite, syncErr, temporary.Close())
	}
	if closeErr := temporary.Close(); closeErr != nil {
		return errors.Join(ErrKeyringWrite, closeErr)
	}
	if renameErr := os.Rename(temporaryPath, path); renameErr != nil {
		return errors.Join(ErrKeyringWrite, renameErr)
	}
	return nil
}
