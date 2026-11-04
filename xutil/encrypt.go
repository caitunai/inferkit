package xutil

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"io"
	"strings"
)

// Encrypt string to base64 crypto using GCM
// The key should be 16 bytes (AES-128) or 32 (AES-256)
func Encrypt(key []byte, text string) string {
	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}

	// Never use more than 2^32 random nonces with a given key because of the risk of a repeat.
	nonce := make([]byte, 12)
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return ""
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}

	ciphertext := aesGCM.Seal(nil, nonce, []byte(text), nil)

	// convert to base64
	return hex.EncodeToString(ciphertext) + "." + hex.EncodeToString(nonce)
}

// Decrypt from base64 to decrypted string
// The key should be 16 bytes (AES-128) or 32 (AES-256)
func Decrypt(key []byte, cryptoText string) string {
	texts := strings.Split(cryptoText, ".")
	if len(texts) != 2 {
		return ""
	}
	data, err := hex.DecodeString(texts[0])
	if err != nil {
		return ""
	}
	nonce, err := hex.DecodeString(texts[1])
	if err != nil {
		return ""
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}

	plaintext, err := aesGCM.Open(nil, nonce, data, nil)
	if err != nil {
		return ""
	}

	return string(plaintext)
}
