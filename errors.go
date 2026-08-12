package hub

import (
	"errors"
	"fmt"
)

var (
	// ErrClosed is returned by [Publish] and [Subscribe] after [Hub.Close].
	ErrClosed = errors.New("hub: closed")
	// ErrNilHub is returned when a nil [Hub] is passed to [Publish] or
	// [Subscribe]. A nil hub is a wiring mistake, and reporting it beats
	// panicking inside a package the caller did not write.
	ErrNilHub = errors.New("hub: hub must not be nil")
	// ErrNilHandler is returned by [Subscribe] when the handler is nil.
	ErrNilHandler = errors.New("hub: handler must not be nil")
	// ErrInvalidTopic is returned when a zero [Topic] is used. Build topics
	// with [NewTopic] or [TypeTopic].
	ErrInvalidTopic = errors.New("hub: topic is not initialized")
	// ErrQueueFull is returned by [Publish] for a subscriber whose queue is
	// full and whose overflow policy is [Fail].
	ErrQueueFull = errors.New("hub: subscriber queue is full")
)

// PanicError reports a handler that panicked. The panic is recovered so it
// cannot take down the publisher or a worker goroutine, and is delivered as
// this error to [WithErrorHandler], to [WithLogger] and — for a [Synchronous]
// subscription — to the publisher.
//
// The recovered value is kept as-is, so a handler that panics with a typed
// value can be examined:
//
//	var pe *hub.PanicError
//	if errors.As(err, &pe) {
//	    log.Printf("%s panicked with %#v\n%s", pe.Topic, pe.Value, pe.Stack)
//	}
type PanicError struct {
	// Topic identifies the topic being delivered when the handler panicked.
	Topic string
	// Value is the value passed to panic.
	Value any
	// Stack is the stack trace captured at the point of recovery. It is the
	// only record of where the panic came from: the goroutine that produced it
	// does not survive to be inspected.
	Stack []byte
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("hub: handler for %s panicked: %v", e.Topic, e.Value)
}

// Unwrap exposes a panic value that is itself an error, so that
// errors.Is/errors.As reach it.
func (e *PanicError) Unwrap() error {
	err, _ := e.Value.(error)

	return err
}
