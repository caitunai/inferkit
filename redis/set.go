package redis

import (
	"context"
	"errors"
	"strconv"

	"github.com/redis/go-redis/v9"
)

var (
	// ErrAddSortItem indicates error to add item to sort set.
	ErrAddSortItem = errors.New("error to add item to sort set")
	// ErrRemoveSortItem indicates error to remove item from sort set.
	ErrRemoveSortItem = errors.New("error to remove item from sort set")
	// ErrGetMinimalSortItem indicates error to get the minimal sort item.
	ErrGetMinimalSortItem = errors.New("error to get the minimal sort item")
	// ErrInvalidSortRange indicates invalid sorted set score range.
	ErrInvalidSortRange = errors.New("invalid sorted set score range")
	// ErrInexactSortScore indicates sorted set score exceeds exact integer range.
	ErrInexactSortScore = errors.New("sorted set score exceeds exact integer range")
)

const maxExactFloatInteger = int64(1 << 53)

// AddSortItem performs the add sort item operation.
func AddSortItem(ctx context.Context, key, item string, value int64) error {
	redisKey, err := prefixedKey(key)
	if err != nil {
		return errors.Join(ErrAddSortItem, err)
	}
	if value > maxExactFloatInteger || value < -maxExactFloatInteger {
		return errors.Join(ErrAddSortItem, ErrInexactSortScore)
	}
	res := GetClient().ZAdd(ctx, redisKey, redis.Z{
		Score:  float64(value),
		Member: item,
	})
	err = res.Err()
	if err != nil {
		return errors.Join(ErrAddSortItem, err)
	}
	return nil
}

// RemoveSortItem remove items include min, max value
func RemoveSortItem(ctx context.Context, key string, minV, maxV int64) error {
	redisKey, err := prefixedKey(key)
	if err != nil {
		return errors.Join(ErrRemoveSortItem, err)
	}
	if minV > maxV {
		return errors.Join(ErrRemoveSortItem, ErrInvalidSortRange)
	}
	res := GetClient().ZRemRangeByScore(
		ctx,
		redisKey,
		strconv.FormatInt(minV, 10),
		strconv.FormatInt(maxV, 10),
	)
	err = res.Err()
	if err != nil {
		return errors.Join(ErrRemoveSortItem, err)
	}
	return nil
}

// GetMinSortItem returns min sort item.
func GetMinSortItem(ctx context.Context, key string) (string, error) {
	redisKey, err := prefixedKey(key)
	if err != nil {
		return "", errors.Join(ErrGetMinimalSortItem, err)
	}
	res := GetClient().ZRange(ctx, redisKey, 0, 0)
	result, err := res.Result()
	if err != nil {
		return "", errors.Join(ErrGetMinimalSortItem, err)
	}
	if len(result) > 0 {
		return result[0], nil
	}
	return "", errors.Join(ErrGetMinimalSortItem, ErrKeyNotFound)
}
