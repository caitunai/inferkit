package redis

import (
	"errors"
	"time"

	"github.com/spf13/viper"
)

var (
	// ErrInvalidKey indicates redis key is empty.
	ErrInvalidKey = errors.New("redis key is empty")
	// ErrInvalidExpiration indicates redis expiration must be positive.
	ErrInvalidExpiration = errors.New("redis expiration must be positive")
	// ErrKeyNotFound indicates redis key not found.
	ErrKeyNotFound = errors.New("redis key not found")
)

// WithPrefix performs the with prefix operation.
func WithPrefix(k string) string {
	prefix := viper.GetString("redis.prefix")
	if prefix != "" {
		return prefix + ":" + k
	}
	return k
}

func prefixedKey(key string) (string, error) {
	if key == "" {
		return "", ErrInvalidKey
	}
	return WithPrefix(key), nil
}

func validateExpiration(expiration time.Duration) error {
	if expiration < time.Millisecond {
		return ErrInvalidExpiration
	}
	return nil
}
