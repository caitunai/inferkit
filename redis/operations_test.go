package redis

import (
	"errors"
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestOperationsRejectInvalidInputsBeforeRedisCall(t *testing.T) {
	ctx := t.Context()
	tests := []struct {
		want error
		run  func() error
		name string
	}{
		{name: "expire empty key", run: func() error { return SetExpire(ctx, "", time.Second) }, want: ErrInvalidKey},
		{name: "expire invalid ttl", run: func() error { return SetExpire(ctx, "key", 0) }, want: ErrInvalidExpiration},
		{name: "list empty values", run: func() error {
			return RightPushWithLimitExpired(ctx, "key", nil, 1, time.Second)
		}, want: ErrInvalidList},
		{name: "list invalid limit", run: func() error {
			return RightPushWithLimitExpired(ctx, "key", []string{"value"}, 0, time.Second)
		}, want: ErrInvalidList},
		{name: "increment invalid ttl", run: func() error {
			_, err := Increment(ctx, "key", 0)
			return err
		}, want: ErrInvalidExpiration},
		{name: "sorted set inexact score", run: func() error {
			return AddSortItem(ctx, "key", "member", maxExactFloatInteger+1)
		}, want: ErrInexactSortScore},
		{name: "sorted set invalid range", run: func() error {
			return RemoveSortItem(ctx, "key", 2, 1)
		}, want: ErrInvalidSortRange},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestWithPrefix(t *testing.T) {
	oldPrefix := viper.GetString("redis.prefix")
	viper.Set("redis.prefix", "test")
	t.Cleanup(func() { viper.Set("redis.prefix", oldPrefix) })

	if got := WithPrefix("key"); got != "test:key" {
		t.Fatalf("WithPrefix() = %q, want %q", got, "test:key")
	}
}
