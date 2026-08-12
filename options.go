package msghub

import (
	"context"
	"log/slog"
)

// options is the resolved configuration of a [Hub].
type options struct {
	queueSize int
	overflow  Overflow
	logger    *slog.Logger
	onError   func(ctx context.Context, topic string, err error)
}

// Option configures a [Hub] at construction. Options set the defaults every
// subscription inherits; a subscription can override them with a [SubOption].
type Option func(*options)

// WithQueueSize sets the default per-subscriber queue depth.
//
// A depth of zero makes every queue a rendezvous: the publisher waits for the
// handler to pick the event up. Negative values are treated as zero.
func WithQueueSize(n int) Option {
	return func(o *options) {
		if n < 0 {
			n = 0
		}
		o.queueSize = n
	}
}

// WithOverflow sets the default policy for a full subscriber queue.
func WithOverflow(p Overflow) Option {
	return func(o *options) { o.overflow = p }
}

// WithLogger sets the logger that records handler errors and panics. A nil
// logger disables logging; pass a handler writing to io.Discard to silence the
// hub in tests without losing [WithErrorHandler].
func WithLogger(l *slog.Logger) Option {
	return func(o *options) { o.logger = l }
}

// WithErrorHandler installs a callback invoked for every handler error and
// every recovered panic, with the topic that was being delivered.
//
// It runs on the goroutine that detected the failure — a subscriber worker for
// an asynchronous handler, the publisher's own goroutine for a synchronous
// one — so it must not block.
func WithErrorHandler(fn func(ctx context.Context, topic string, err error)) Option {
	return func(o *options) { o.onError = fn }
}

// subOptions is the resolved configuration of one subscription. The pointer
// fields distinguish "not set" from "set to the zero value", so that
// WithSubQueueSize(0) can request a rendezvous queue on a hub whose default is
// larger.
type subOptions struct {
	queueSize *int
	overflow  *Overflow
	sync      bool
}

// SubOption configures a single subscription, overriding the hub default.
type SubOption func(*subOptions)

// WithSubQueueSize overrides the queue depth for this subscription.
func WithSubQueueSize(n int) SubOption {
	return func(o *subOptions) {
		if n < 0 {
			n = 0
		}
		o.queueSize = &n
	}
}

// WithSubOverflow overrides the full-queue policy for this subscription.
func WithSubOverflow(p Overflow) SubOption {
	return func(o *subOptions) { o.overflow = &p }
}

// Synchronous runs the handler inline in [Publish] rather than on a queue, and
// returns its error to the publisher.
//
// Use it when the handler must finish — or fail — before the publisher
// proceeds: a validation step, a write that the next statement depends on. The
// cost is the publisher's time: a synchronous handler blocks it for as long as
// it runs, and [WithQueueSize] and [Overflow] no longer apply, because there is
// no queue to fill.
func Synchronous() SubOption {
	return func(o *subOptions) { o.sync = true }
}

// resolve folds the subscription options over the hub defaults.
func (o options) resolve(subOpts []SubOption) (size int, policy Overflow, sync bool) {
	var s subOptions
	for _, apply := range subOpts {
		apply(&s)
	}

	size, policy = o.queueSize, o.overflow
	if s.queueSize != nil {
		size = *s.queueSize
	}
	if s.overflow != nil {
		policy = *s.overflow
	}

	return size, policy, s.sync
}
