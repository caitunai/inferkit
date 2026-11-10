package pulse

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog/log"
)

const reportTaskName = "pulse_report"

// Enqueue starts one protected task without waiting for network I/O. Request
// cancellation does not cancel accepted telemetry; only the process owner,
// reporting timeout or Close can cancel it. No request or Gin state is retained.
func (c *Client) Enqueue(ctx context.Context, name string, admittedAt time.Time, elapsed time.Duration) error {
	if c == nil {
		return nil
	}
	if name != ImageFilter && name != ImageOCR {
		return ErrEvent
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.admit(); err != nil {
		return err
	}
	// Copy only the logger onto the owned context, rather than retaining request values.
	logger := *log.Ctx(ctx)
	taskCtx, cancel := context.WithTimeout(logger.WithContext(c.owner), c.config.Timeout)
	c.tasks.Go(taskCtx, func(taskCtx context.Context) { //nolint:contextcheck // Accepted telemetry belongs to the client lifecycle and must survive cancellation of the HTTP request context.
		defer cancel()
		defer func() { <-c.slots }()
		if err := c.Report(taskCtx, name, admittedAt, elapsed); err != nil {
			log.Ctx(taskCtx).Warn().Err(err).Str("event_name", name).Msg("Pulse usage reporting failed")
		}
	})
	return nil
}

// admit runs with mu held so no Wait can race with task registration during Close.
func (c *Client) admit() error {
	if c.closed {
		return ErrClosed
	}
	if err := c.owner.Err(); err != nil {
		return errors.Join(ErrClosed, err)
	}
	select {
	case c.slots <- struct{}{}:
		return nil
	default:
		return ErrBusy
	}
}

// Close stops admission, drains accepted tasks, then releases the HTTP pool.
// After ShutdownTimeout, in-flight HTTP operations are canceled. Close is nil-safe
// and concurrent calls share one shutdown lifecycle.
func (c *Client) Close() {
	if c != nil {
		c.once.Do(c.shutdown)
	}
}

func (c *Client) shutdown() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	timer := time.AfterFunc(c.config.ShutdownTimeout, c.cancel)
	c.tasks.Wait()
	if !timer.Stop() {
		log.Ctx(c.owner).Warn().Msg("Pulse shutdown timeout canceled outstanding reports")
	}
	c.cancel()
	c.http.GetClient().CloseIdleConnections()
}
