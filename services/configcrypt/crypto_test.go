package configcrypt

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestManagerEncryptDecryptAndAuthenticateContext(t *testing.T) {
	manager := testManager(t, "key-v1")
	plaintext := []byte(`{"database":{"password":"secret"}}`)
	context := "namespace=1|environment=2|version=0|kind=draft-config"

	first, err := manager.Encrypt(plaintext, context)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	second, err := manager.Encrypt(plaintext, context)
	if err != nil {
		t.Fatalf("Encrypt() second error = %v", err)
	}
	if first == second || !strings.HasPrefix(first, ciphertextPrefix) {
		t.Fatalf("Encrypt() did not produce randomized versioned ciphertext")
	}

	decrypted, encrypted, err := manager.Decrypt(first, context)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if !encrypted || string(decrypted) != string(plaintext) {
		t.Fatalf("Decrypt() = %q, %t", decrypted, encrypted)
	}
	if _, _, operationErr := manager.Decrypt(first, context+"-changed"); !errors.Is(operationErr, ErrDecrypt) {
		t.Fatalf("Decrypt() context error = %v, want ErrDecrypt", operationErr)
	}
}

//nolint:cyclop,gocognit // This end-to-end test keeps one setup and assertion lifecycle visible.
func TestManagerReencryptWrapsExistingDataKeyWithActiveKey(t *testing.T) {
	directory := t.TempDir()
	keyringPath := filepath.Join(directory, "keys.json")
	if err := GenerateFileKey(keyringPath, "key-old"); err != nil {
		t.Fatalf("GenerateFileKey() old error = %v", err)
	}
	oldManager, err := NewManager(Settings{Enabled: true, Provider: ProviderFile, ActiveKeyID: "key-old", KeyringPath: keyringPath})
	if err != nil {
		t.Fatalf("NewManager() old error = %v", err)
	}
	context := "namespace=3|environment=7|version=4|kind=release-config"
	stored, err := oldManager.Encrypt([]byte(`{"token":"value"}`), context)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	original, _, err := parseEnvelope(stored)
	if err != nil {
		t.Fatalf("parseEnvelope() error = %v", err)
	}

	if operationErr := GenerateFileKey(keyringPath, "key-new"); operationErr != nil {
		t.Fatalf("GenerateFileKey() new error = %v", operationErr)
	}
	newManager, err := NewManager(Settings{Enabled: true, Provider: ProviderFile, ActiveKeyID: "key-new", KeyringPath: keyringPath})
	if err != nil {
		t.Fatalf("NewManager() new error = %v", err)
	}
	updated, changed, err := newManager.Reencrypt(stored, context)
	if err != nil {
		t.Fatalf("Reencrypt() error = %v", err)
	}
	if !changed {
		t.Fatal("Reencrypt() changed = false")
	}
	rotated, _, err := parseEnvelope(updated)
	if err != nil {
		t.Fatalf("parseEnvelope() rotated error = %v", err)
	}
	if rotated.KeyID != "key-new" || rotated.Ciphertext != original.Ciphertext || rotated.DataNonce != original.DataNonce {
		t.Fatalf("Reencrypt() rewrote payload instead of only wrapping its data key")
	}
	decrypted, _, err := newManager.Decrypt(updated, context)
	if err != nil || string(decrypted) != `{"token":"value"}` {
		t.Fatalf("Decrypt() rotated = %q, %v", decrypted, err)
	}
	if _, _, operationErr := oldManager.Decrypt(updated, context); !errors.Is(operationErr, ErrKeyNotFound) {
		t.Fatalf("Decrypt() with old keyring error = %v, want ErrKeyNotFound", operationErr)
	}
}

func TestManagerReencryptsLegacyPlaintextAndRejectsTampering(t *testing.T) {
	manager := testManager(t, "key-v1")
	context := "namespace=1|environment=1|version=0|kind=draft-config"
	updated, changed, err := manager.Reencrypt(`{"legacy":true}`, context)
	if err != nil || !changed {
		t.Fatalf("Reencrypt() = %t, %v", changed, err)
	}
	decrypted, _, err := manager.Decrypt(updated, context)
	if err != nil || string(decrypted) != `{"legacy":true}` {
		t.Fatalf("Decrypt() legacy = %q, %v", decrypted, err)
	}

	tampered := updated[:len(updated)-1] + "A"
	if _, _, operationErr := manager.Decrypt(tampered, context); !errors.Is(operationErr, ErrDecrypt) {
		t.Fatalf("Decrypt() tampered error = %v, want ErrDecrypt", operationErr)
	}
}

func TestDisabledManagerPassesPlaintextButRejectsCiphertext(t *testing.T) {
	disabled, err := NewManager(Settings{})
	if err != nil {
		t.Fatalf("NewManager() disabled error = %v", err)
	}
	stored, err := disabled.Encrypt([]byte(`{"plain":true}`), "context")
	if err != nil || stored != `{"plain":true}` {
		t.Fatalf("Encrypt() disabled = %q, %v", stored, err)
	}
	plaintext, encrypted, err := disabled.Decrypt(stored, "context")
	if err != nil || encrypted || string(plaintext) != stored {
		t.Fatalf("Decrypt() disabled plaintext = %q, %t, %v", plaintext, encrypted, err)
	}

	enabled := testManager(t, "key-v1")
	ciphertext, err := enabled.Encrypt([]byte("secret"), "context")
	if err != nil {
		t.Fatalf("Encrypt() enabled error = %v", err)
	}
	if _, _, operationErr := disabled.Decrypt(ciphertext, "context"); !errors.Is(operationErr, ErrDisabled) {
		t.Fatalf("Decrypt() disabled ciphertext error = %v, want ErrDisabled", operationErr)
	}
}

//nolint:cyclop,gocognit // This end-to-end test keeps one setup and assertion lifecycle visible.
func TestGenerateFileKeyUsesRestrictedPermissionsAndPreservesKeys(t *testing.T) {
	keyringPath := filepath.Join(t.TempDir(), "keys.json")
	if err := GenerateFileKey(keyringPath, "key-one"); err != nil {
		t.Fatalf("GenerateFileKey() error = %v", err)
	}
	if runtime.GOOS != osWindows {
		info, err := os.Stat(keyringPath)
		if err != nil {
			t.Fatalf("Stat() error = %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("keyring mode = %o, want 600", info.Mode().Perm())
		}
	}
	if err := GenerateFileKey(keyringPath, "key-two"); err != nil {
		t.Fatalf("GenerateFileKey() append error = %v", err)
	}
	keys, err := loadFileKeys(keyringPath)
	if err != nil {
		t.Fatalf("loadFileKeys() error = %v", err)
	}
	if len(keys) != 2 || len(keys["key-one"]) != keyBytes || len(keys["key-two"]) != keyBytes {
		t.Fatalf("loadFileKeys() keys = %#v", keys)
	}
	if operationErr := GenerateFileKey(keyringPath, "key-one"); !errors.Is(operationErr, ErrKeyAlreadyExists) {
		t.Fatalf("GenerateFileKey() duplicate error = %v, want ErrKeyAlreadyExists", operationErr)
	}
}

func TestNewManagerRejectsInsecureKeyringPermissions(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("Windows does not expose POSIX keyring permissions")
	}
	keyringPath := filepath.Join(t.TempDir(), "keys.json")
	if err := GenerateFileKey(keyringPath, "key-v1"); err != nil {
		t.Fatalf("GenerateFileKey() error = %v", err)
	}
	if err := os.Chmod(keyringPath, 0o644); err != nil { // #nosec G302 -- this test intentionally creates insecure permissions.
		t.Fatalf("Chmod() error = %v", err)
	}
	_, err := NewManager(Settings{Enabled: true, Provider: ProviderFile, ActiveKeyID: "key-v1", KeyringPath: keyringPath})
	if !errors.Is(err, ErrInsecureKeyring) {
		t.Fatalf("NewManager() error = %v, want ErrInsecureKeyring", err)
	}
}

func testManager(t *testing.T, keyID string) *Manager {
	t.Helper()
	keyringPath := filepath.Join(t.TempDir(), "keys.json")
	if err := GenerateFileKey(keyringPath, keyID); err != nil {
		t.Fatalf("GenerateFileKey() error = %v", err)
	}
	manager, err := NewManager(Settings{Enabled: true, Provider: ProviderFile, ActiveKeyID: keyID, KeyringPath: keyringPath})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return manager
}
