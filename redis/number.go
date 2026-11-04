package redis

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	// ErrIncrementNumber indicates error to increment number.
	ErrIncrementNumber = errors.New("error to increment number")
	// ErrDecrementNumber indicates error to decrement number.
	ErrDecrementNumber = errors.New("error to decrement number")
)

var changeNumberScript = redis.NewScript(
	"local value = redis.call('INCRBY', KEYS[1], ARGV[1])\n" +
		"redis.call('PEXPIRE', KEYS[1], ARGV[2])\n" +
		"return value",
)

// Increment performs the increment operation.
func Increment(ctx context.Context, key string, t time.Duration) (int64, error) {
	return changeNumber(ctx, key, 1, t, ErrIncrementNumber)
}

// Decrement performs the decrement operation.
func Decrement(ctx context.Context, key string, t time.Duration) (int64, error) {
	return changeNumber(ctx, key, -1, t, ErrDecrementNumber)
}

func changeNumber(
	ctx context.Context,
	key string,
	delta int64,
	expiration time.Duration,
	operationErr error,
) (int64, error) {
	redisKey, err := prefixedKey(key)
	if err != nil {
		return 0, errors.Join(operationErr, err)
	}
	if validationErr := validateExpiration(expiration); validationErr != nil {
		return 0, errors.Join(operationErr, validationErr)
	}
	result, err := changeNumberScript.Run(
		ctx,
		GetClient(),
		[]string{redisKey},
		strconv.FormatInt(delta, 10),
		strconv.FormatInt(expiration.Milliseconds(), 10),
	).Int64()
	if err != nil {
		return 0, errors.Join(operationErr, err)
	}
	return result, nil
}
