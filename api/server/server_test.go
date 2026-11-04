package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/caitun/inferkit/xutil"
)

func TestHTTPTimeoutsDefaultToUnlimited(t *testing.T) {
	keys := []string{
		"server.readHeaderTimeout",
		"server.readTimeout",
		"server.writeTimeout",
		"server.idleTimeout",
	}
	for _, key := range keys {
		if timeout := configuredDuration(key, noHTTPTimeout); timeout != noHTTPTimeout {
			t.Errorf("configuredDuration(%q) = %s, want unlimited", key, timeout)
		}
	}
}

func TestServerTaskRecoversPanic(t *testing.T) {
	panicEvents := make(chan error, 1)
	unsubscribe := xutil.OnPanic(httpServerTaskName, func(_ context.Context, _ string, err error) {
		panicEvents <- err
	})
	t.Cleanup(unsubscribe)

	startServerTask(t.Context(), func(context.Context) {
		panic("test panic")
	})

	select {
	case err := <-panicEvents:
		if !errors.Is(err, xutil.ErrPanic) {
			t.Fatalf("server task error = %v, want xutil.ErrPanic", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for server panic recovery")
	}
}
