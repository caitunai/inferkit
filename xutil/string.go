package xutil

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

var stripTagRegx = regexp.MustCompile(`<(.|\n)*?>`)

// SHA256 returns the lowercase hexadecimal SHA-256 digest of value.
// It is not suitable for password hashing; use a password-specific KDF for passwords.
func SHA256(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// StripTags performs the strip tags operation.
func StripTags(content string) string {
	return stripTagRegx.ReplaceAllString(content, "")
}

// MaskEmail performs the mask email operation.
func MaskEmail(i string) string {
	l := len(i)
	if l == 0 {
		return ""
	}

	tmp := strings.Split(i, "@")
	if len(tmp) == 1 {
		return MaskString(i)
	}

	addr := tmp[0]
	domain := tmp[1]

	return MaskString(addr) + "@" + domain
}

// MaskString performs the mask string operation.
func MaskString(s string) string {
	list := strings.Split(s, "")
	for i, s2 := range list {
		if i%2 == 1 {
			list[i] = "*"
		} else {
			list[i] = s2
		}
	}
	return strings.Join(list, "")
}

// SubString performs the sub string operation.
func SubString(s string, start, length int) string {
	r := []rune(s)
	if len(r) <= length {
		return s
	}
	return string(r[start : length+start])
}

// SplitByWidth performs the split by width operation.
func SplitByWidth(str string, size int) []string {
	chars := []rune(str)
	strLength := len(chars)
	var splited []string
	var stop int
	for i := 0; i < strLength; i += size {
		stop = min(i+size, strLength)
		splited = append(splited, string(chars[i:stop]))
	}
	return splited
}

// RandomString performs the random string operation.
func RandomString(length int) string {
	b := make([]byte, length/2)
	_, err := rand.Read(b)
	if err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
