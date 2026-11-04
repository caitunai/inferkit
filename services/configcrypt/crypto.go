package configcrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

const (
	ciphertextPrefix = "gbce:v1:"
	cipherVersion    = 1
	cipherAlgorithm  = "AES-256-GCM"
	aadPayloadPrefix = "configcrypt/config-center|v1|payload|"
	aadDataKeyPrefix = "configcrypt/config-center|v1|dek|"
)

type envelope struct {
	Algorithm  string `json:"alg"`
	Ciphertext string `json:"ct"`
	DataNonce  string `json:"nonce"`
	KeyID      string `json:"kid"`
	WrappedDEK string `json:"dek"`
	WrapNonce  string `json:"dek_nonce"`
	Version    int    `json:"v"`
}

// Manager represents manager data.
type Manager struct {
	keys        map[string][]byte
	activeKeyID string
	enabled     bool
}

var (
	defaultManager   = &Manager{}
	defaultManagerMu sync.RWMutex
)

// NewManager creates a new manager.
func NewManager(settings Settings) (*Manager, error) {
	if !settings.Enabled {
		return &Manager{}, nil
	}
	if settings.Provider != ProviderFile || !keyIDPattern.MatchString(settings.ActiveKeyID) {
		return nil, ErrInvalidSettings
	}
	keys, err := loadFileKeys(settings.KeyringPath)
	if err != nil {
		return nil, err
	}
	if _, exists := keys[settings.ActiveKeyID]; !exists {
		return nil, ErrKeyNotFound
	}
	return &Manager{enabled: true, activeKeyID: settings.ActiveKeyID, keys: keys}, nil
}

// Configure performs the configure operation.
func Configure(settings Settings) error {
	manager, err := NewManager(settings)
	if err != nil {
		return err
	}
	defaultManagerMu.Lock()
	defaultManager = manager
	defaultManagerMu.Unlock()
	return nil
}

// Enabled performs the enabled operation.
func Enabled() bool {
	return currentManager().enabled
}

// ActiveKeyID performs the active key id operation.
func ActiveKeyID() string {
	return currentManager().activeKeyID
}

// Encrypt performs the encrypt operation.
func Encrypt(plaintext []byte, context string) (string, error) {
	return currentManager().Encrypt(plaintext, context)
}

// Decrypt performs the decrypt operation.
func Decrypt(stored, context string) ([]byte, bool, error) {
	return currentManager().Decrypt(stored, context)
}

// Reencrypt performs the reencrypt operation.
func Reencrypt(stored, context string) (string, bool, error) {
	return currentManager().Reencrypt(stored, context)
}

func currentManager() *Manager {
	defaultManagerMu.RLock()
	manager := defaultManager
	defaultManagerMu.RUnlock()
	return manager
}

// Encrypt performs the encrypt operation.
func (m *Manager) Encrypt(plaintext []byte, context string) (string, error) {
	if !m.enabled {
		return string(plaintext), nil
	}
	dataKey := make([]byte, keyBytes)
	if _, err := io.ReadFull(rand.Reader, dataKey); err != nil {
		return "", errors.Join(ErrEncrypt, err)
	}
	ciphertext, dataNonce, err := seal(dataKey, plaintext, payloadAAD(context))
	if err != nil {
		return "", errors.Join(ErrEncrypt, err)
	}
	wrappedKey, wrapNonce, err := seal(m.keys[m.activeKeyID], dataKey, wrapAAD(context, m.activeKeyID))
	if err != nil {
		return "", errors.Join(ErrEncrypt, err)
	}
	return marshalEnvelope(envelope{
		Version:    cipherVersion,
		Algorithm:  cipherAlgorithm,
		KeyID:      m.activeKeyID,
		WrappedDEK: encodeBytes(wrappedKey),
		WrapNonce:  encodeBytes(wrapNonce),
		DataNonce:  encodeBytes(dataNonce),
		Ciphertext: encodeBytes(ciphertext),
	})
}

// Decrypt performs the decrypt operation.
func (m *Manager) Decrypt(stored, context string) ([]byte, bool, error) {
	parsed, encrypted, err := parseEnvelope(stored)
	if err != nil {
		return nil, encrypted, err
	}
	if !encrypted {
		return []byte(stored), false, nil
	}
	if !m.enabled {
		return nil, true, ErrDisabled
	}
	plaintext, err := m.decryptEnvelope(parsed, context)
	if err != nil {
		return nil, true, err
	}
	return plaintext, true, nil
}

// Reencrypt performs the reencrypt operation.
func (m *Manager) Reencrypt(stored, context string) (string, bool, error) {
	if !m.enabled {
		return "", false, ErrDisabled
	}
	parsed, encrypted, err := parseEnvelope(stored)
	if err != nil {
		return "", false, err
	}
	if !encrypted {
		updated, encryptErr := m.Encrypt([]byte(stored), context)
		return updated, true, encryptErr
	}
	dataKey, err := m.unwrapDataKey(parsed, context)
	if err != nil {
		return "", false, err
	}
	if _, verifyErr := open(dataKey, parsed.Ciphertext, parsed.DataNonce, payloadAAD(context)); verifyErr != nil {
		return "", false, errors.Join(ErrDecrypt, verifyErr)
	}
	if parsed.KeyID == m.activeKeyID {
		return stored, false, nil
	}
	wrappedKey, wrapNonce, err := seal(m.keys[m.activeKeyID], dataKey, wrapAAD(context, m.activeKeyID))
	if err != nil {
		return "", false, errors.Join(ErrEncrypt, err)
	}
	parsed.KeyID = m.activeKeyID
	parsed.WrappedDEK = encodeBytes(wrappedKey)
	parsed.WrapNonce = encodeBytes(wrapNonce)
	updated, err := marshalEnvelope(*parsed)
	if err != nil {
		return "", false, err
	}
	return updated, true, nil
}

func (m *Manager) decryptEnvelope(parsed *envelope, context string) ([]byte, error) {
	dataKey, err := m.unwrapDataKey(parsed, context)
	if err != nil {
		return nil, err
	}
	plaintext, err := open(dataKey, parsed.Ciphertext, parsed.DataNonce, payloadAAD(context))
	if err != nil {
		return nil, errors.Join(ErrDecrypt, err)
	}
	return plaintext, nil
}

func (m *Manager) unwrapDataKey(parsed *envelope, context string) ([]byte, error) {
	key, exists := m.keys[parsed.KeyID]
	if !exists {
		return nil, ErrKeyNotFound
	}
	dataKey, err := open(key, parsed.WrappedDEK, parsed.WrapNonce, wrapAAD(context, parsed.KeyID))
	if err != nil {
		return nil, errors.Join(ErrDecrypt, err)
	}
	if len(dataKey) != keyBytes {
		return nil, ErrDecrypt
	}
	return dataKey, nil
}

func seal(key, plaintext, additionalData []byte) ([]byte, []byte, error) {
	aead, err := newGCM(key)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, randomErr := io.ReadFull(rand.Reader, nonce); randomErr != nil {
		return nil, nil, errors.Join(ErrEncrypt, randomErr)
	}
	return aead.Seal(nil, nonce, plaintext, additionalData), nonce, nil
}

func open(key []byte, encodedCiphertext, encodedNonce string, additionalData []byte) ([]byte, error) {
	ciphertext, err := decodeBytes(encodedCiphertext)
	if err != nil {
		return nil, err
	}
	nonce, err := decodeBytes(encodedNonce)
	if err != nil {
		return nil, err
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, ErrDecrypt
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, additionalData)
	if err != nil {
		return nil, errors.Join(ErrDecrypt, err)
	}
	return plaintext, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.Join(ErrCipherSetup, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.Join(ErrCipherSetup, err)
	}
	return aead, nil
}

func marshalEnvelope(document envelope) (string, error) {
	raw, err := json.Marshal(document)
	if err != nil {
		return "", errors.Join(ErrEncrypt, err)
	}
	return ciphertextPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func parseEnvelope(stored string) (*envelope, bool, error) {
	if len(stored) < len(ciphertextPrefix) || stored[:len(ciphertextPrefix)] != ciphertextPrefix {
		return nil, false, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(stored[len(ciphertextPrefix):])
	if err != nil {
		return nil, true, errors.Join(ErrDecrypt, err)
	}
	var document envelope
	if decodeErr := json.Unmarshal(raw, &document); decodeErr != nil {
		return nil, true, errors.Join(ErrDecrypt, decodeErr)
	}
	if !validEnvelope(document) {
		return nil, true, ErrDecrypt
	}
	return &document, true, nil
}

func validEnvelope(document envelope) bool {
	if document.Version != cipherVersion || document.Algorithm != cipherAlgorithm {
		return false
	}
	if !keyIDPattern.MatchString(document.KeyID) {
		return false
	}
	return document.WrappedDEK != "" && document.WrapNonce != "" && document.DataNonce != "" && document.Ciphertext != ""
}

func payloadAAD(context string) []byte {
	return []byte(aadPayloadPrefix + context)
}

func wrapAAD(context, keyID string) []byte {
	return []byte(aadDataKeyPrefix + keyID + "|" + context)
}

func encodeBytes(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

func decodeBytes(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, errors.Join(ErrDecrypt, err)
	}
	return decoded, nil
}
