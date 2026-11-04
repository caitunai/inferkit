package redis

import (
	"context"
	"errors"
	"time"
)

// ErrSetExpire indicates set redis key expiration failed.
var ErrSetExpire = errors.New("set redis key expiration failed")

// SetExpire sets a relative expiration on a prefixed application key.
func SetExpire(ctx context.Context, key string, expiration time.Duration) error {
	redisKey, err := prefixedKey(key)
	if err != nil {
		return errors.Join(ErrSetExpire, err)
	}
	if validationErr := validateExpiration(expiration); validationErr != nil {
		return errors.Join(ErrSetExpire, validationErr)
	}
	exists, err := GetClient().PExpire(ctx, redisKey, expiration).Result()
	if err != nil {
		return errors.Join(ErrSetExpire, err)
	}
	if !exists {
		return errors.Join(ErrSetExpire, ErrKeyNotFound)
	}
	return nil
}
