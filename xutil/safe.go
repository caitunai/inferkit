package xutil

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"runtime/debug"
	"slices"
	"sync"

	"github.com/rs/zerolog/log"
	"golang.org/x/sync/errgroup"
)

const (
	// All subscribes a panic handler to every named task group.
	All = "*"
)

var (
	// ErrPanic indicates that a protected goroutine panicked.
	ErrPanic           = errors.New("goroutine panicked")
	panicSubscriptions = subscriptionStore{
		handlers: make(map[string]map[uint64]PanicHandler),
	}
)

// Task is a protected goroutine task that does not return an error.
type Task func(context.Context)

// ErrTask is a protected goroutine task that may return an error.
type ErrTask func(context.Context) error

// PanicHandler receives panic errors for a subscribed task-group name.
// Handlers must return quickly and must not block indefinitely.
type PanicHandler func(context.Context, string, error)

// Group starts and waits for protected tasks that do not return errors.
type Group struct {
	name  string
	group sync.WaitGroup
}

// ErrorGroup starts protected error-returning tasks with shared cancellation.
type ErrorGroup struct {
	ctx   context.Context //nolint:containedctx // ErrorGroup owns the derived context for its complete task lifecycle.
	group *errgroup.Group
	name  string
}

type panicError struct {
	cause  error
	name   string
	reason string
}

func (e *panicError) Error() string {
	return ErrPanic.Error() + ": " + e.name + ": " + e.reason
}

func (e *panicError) Is(target error) bool {
	return target == ErrPanic || errors.Is(e.cause, target)
}

func (e *panicError) Unwrap() error {
	return e.cause
}

type subscriptionStore struct {
	handlers map[string]map[uint64]PanicHandler
	mu       sync.RWMutex
	nextID   uint64
}

// Go starts one independent protected goroutine.
// Use WaitGroup or ErrGroup when the caller owns the task lifecycle.
func Go(ctx context.Context, name string, task Task) {
	go func() {
		run(ctx, name, func() {
			task(ctx)
		})
	}()
}

// WaitGroup creates a named protected task group.
func WaitGroup(name string) *Group {
	return &Group{name: name}
}

// Go starts a protected task and returns the group for chaining.
func (g *Group) Go(ctx context.Context, task Task) *Group {
	g.group.Go(func() {
		run(ctx, g.name, func() {
			task(ctx)
		})
	})
	return g
}

// Wait blocks until every task in the group has returned.
func (g *Group) Wait() {
	g.group.Wait()
}

// ErrGroup creates a named protected error group with shared cancellation.
func ErrGroup(ctx context.Context, name string) *ErrorGroup {
	group, groupContext := errgroup.WithContext(ctx)

	return &ErrorGroup{
		ctx:   groupContext,
		group: group,
		name:  name,
	}
}

// Go starts a protected error-returning task and returns the group for chaining.
func (g *ErrorGroup) Go(task ErrTask) *ErrorGroup {
	g.group.Go(func() error {
		return runErr(g.ctx, g.name, func() error {
			return task(g.ctx)
		})
	})

	return g
}

// Limit restricts the number of active tasks. Call it before starting tasks.
func (g *ErrorGroup) Limit(limit int) *ErrorGroup {
	g.group.SetLimit(limit)

	return g
}

// Wait blocks until every task has returned and reports the first error.
func (g *ErrorGroup) Wait() error {
	return g.group.Wait() //nolint:wrapcheck // Preserve the task error; errgroup only coordinates its lifecycle.
}

// OnPanic subscribes handler to one task-group name. Use All to receive every
// panic. The returned unsubscribe function is safe to call more than once.
func OnPanic(name string, handler PanicHandler) func() {
	if handler == nil {
		return func() {}
	}

	panicSubscriptions.mu.Lock()
	panicSubscriptions.nextID++
	id := panicSubscriptions.nextID
	if panicSubscriptions.handlers[name] == nil {
		panicSubscriptions.handlers[name] = make(map[uint64]PanicHandler)
	}
	panicSubscriptions.handlers[name][id] = handler
	panicSubscriptions.mu.Unlock()

	return sync.OnceFunc(func() {
		panicSubscriptions.mu.Lock()
		defer panicSubscriptions.mu.Unlock()
		delete(panicSubscriptions.handlers[name], id)
		if len(panicSubscriptions.handlers[name]) == 0 {
			delete(panicSubscriptions.handlers, name)
		}
	})
}

func run(ctx context.Context, name string, task func()) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}

		panicErr := newPanicError(name, recovered)
		reportPanic(ctx, name, recovered, panicErr)
	}()

	task()
}

func runErr(ctx context.Context, name string, task func() error) (returnErr error) {
	defer func() {
		recovered := recover()
		if recovered != nil {
			panicErr := newPanicError(name, recovered)
			reportPanic(ctx, name, recovered, panicErr)
			returnErr = panicErr
		}
	}()

	return task()
}

func reportPanic(ctx context.Context, name string, recovered any, panicErr error) {
	log.Ctx(ctx).Error().
		Err(panicErr).
		Str("task", name).
		Str("panic_type", fmt.Sprintf("%T", recovered)).
		Bytes("stack", debug.Stack()).
		Msg("goroutine panic recovered")
	panicSubscriptions.notify(ctx, name, panicErr)
}

func newPanicError(name string, recovered any) error {
	panicErr := &panicError{name: name, reason: fmt.Sprint(recovered)}
	if cause, ok := recovered.(error); ok {
		panicErr.cause = cause
	}
	return panicErr
}

func (subscriptions *subscriptionStore) notify(ctx context.Context, name string, err error) {
	handlers := subscriptions.matchingHandlers(name)
	for _, handler := range handlers {
		notifyHandler(ctx, name, err, handler)
	}
}

func (subscriptions *subscriptionStore) matchingHandlers(name string) []PanicHandler {
	subscriptions.mu.RLock()
	defer subscriptions.mu.RUnlock()

	count := len(subscriptions.handlers[name])
	if name != All {
		count += len(subscriptions.handlers[All])
	}
	handlers := make([]PanicHandler, 0, count)
	handlers = append(handlers, slices.Collect(maps.Values(subscriptions.handlers[name]))...)
	if name != All {
		handlers = append(handlers, slices.Collect(maps.Values(subscriptions.handlers[All]))...)
	}
	return handlers
}

func notifyHandler(ctx context.Context, name string, err error, handler PanicHandler) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Ctx(ctx).Error().
				Str("task", name).
				Str("reason", fmt.Sprint(recovered)).
				Str("panic_type", fmt.Sprintf("%T", recovered)).
				Bytes("stack", debug.Stack()).
				Msg("goroutine panic handler panicked")
		}
	}()
	handler(ctx, name, err)
}
