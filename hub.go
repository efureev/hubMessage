package hub

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/efureev/appmod"
)

// MessageHub implements publish/subscribe messaging paradigm
type MessageHub interface {
	appmod.AppModule

	Publish(topicName topic, args ...interface{})
	Close(topicName topic)
	Subscribe(topicName topic, fn interface{}) error
	Unsubscribe(topicName topic, fn interface{}) error
	Topics() []topic
	Topic(topicName topic) ([]*handler, error)
	Wait()
}

type hub struct {
	appmod.BaseAppModule

	mtx sync.RWMutex

	channels channelsMap

	// pending tracks the number of in-flight messages. It is guarded by
	// pendingMtx and signaled via pendingCond so that Wait() may be called
	// concurrently with Publish() without the misuse restrictions of
	// sync.WaitGroup (where Add must happen-before Wait on a zero counter).
	pendingMtx  sync.Mutex
	pendingCond *sync.Cond
	pending     int
}

var (
	instance    MessageHub
	instanceMtx sync.Mutex
)

type topic string
type channelsMap map[topic][]*handler

type handler struct {
	ctx      context.Context
	callback reflect.Value
	cancel   context.CancelFunc
	queue    chan []reflect.Value
}

// Publish publishes arguments to the given topic subscribers.
//
// A snapshot of the topic handlers is taken under the read lock, and the
// actual (potentially blocking) delivery happens after the lock is released.
// This prevents a slow subscriber from blocking concurrent Subscribe/Close
// calls and avoids a deadlock when a handler subscribes/unsubscribes from
// within its own callback.
func (h *hub) Publish(topicName topic, args ...interface{}) {
	rArgs := buildHandlerArgs(args)

	h.mtx.RLock()
	hs := h.channels[topicName]
	snapshot := make([]*handler, len(hs))
	copy(snapshot, hs)
	h.mtx.RUnlock()

	for _, hndr := range snapshot {
		h.addPending(1)

		// Deliver, but bail out if the handler has been canceled
		// (Unsubscribe/Close) so we neither block forever nor send on a
		// goroutine that has already stopped.
		select {
		case hndr.queue <- rArgs:
		case <-hndr.ctx.Done():
			h.donePending()
		}
	}
}

// Subscribe subscribes to the given topic
func (h *hub) Subscribe(topicName topic, fn interface{}) error {
	if fn == nil {
		return errors.New("handler is nil")
	}

	rt := reflect.TypeOf(fn)
	if rt.Kind() != reflect.Func {
		return fmt.Errorf("%s is not a reflect.Func", rt)
	}

	ctx, cancel := context.WithCancel(context.Background())

	hndr := &handler{
		callback: reflect.ValueOf(fn),
		ctx:      ctx,
		cancel:   cancel,
		queue:    make(chan []reflect.Value),
	}

	go func() {
		for {
			select {
			case args, ok := <-hndr.queue:
				if !ok {
					return
				}
				h.dispatch(hndr, args)
			case <-hndr.ctx.Done():
				return
			}
		}
	}()

	h.mtx.Lock()
	defer h.mtx.Unlock()

	h.channels[topicName] = append(h.channels[topicName], hndr)

	return nil
}

// dispatch invokes the handler callback for a single message and signals
// completion of the in-flight message. A panic inside the user callback is
// recovered so it cannot leak the pending counter and block Wait() forever.
func (h *hub) dispatch(hndr *handler, args []reflect.Value) {
	defer h.donePending()
	defer func() {
		_ = recover()
	}()

	hndr.callback.Call(args)
}

// addPending increments the in-flight message counter.
func (h *hub) addPending(n int) {
	h.pendingMtx.Lock()
	h.pending += n
	h.pendingMtx.Unlock()
}

// donePending decrements the in-flight message counter and wakes up any
// goroutine blocked in Wait() once the counter reaches zero.
func (h *hub) donePending() {
	h.pendingMtx.Lock()
	h.pending--
	if h.pending <= 0 {
		h.pending = 0
		h.pendingCond.Broadcast()
	}
	h.pendingMtx.Unlock()
}

// Unsubscribe unsubscribe handler from the given topic
func (h *hub) Unsubscribe(topicName topic, fn interface{}) error {
	rv := reflect.ValueOf(fn)
	if rv.Kind() != reflect.Func {
		return fmt.Errorf("%s is not a reflect.Func", rv.Type())
	}

	h.mtx.Lock()
	defer h.mtx.Unlock()

	handlers, ok := h.channels[topicName]
	if !ok {
		return fmt.Errorf("topic %s doesn't exist", topicName)
	}

	remaining := handlers[:0]
	for _, ch := range handlers {
		if ch.callback.Pointer() == rv.Pointer() {
			ch.cancel()
			continue
		}
		remaining = append(remaining, ch)
	}
	h.channels[topicName] = remaining

	return nil
}

// Topics return topic list
func (h *hub) Topics() (tt []topic) {
	h.mtx.RLock()
	defer h.mtx.RUnlock()

	for t := range h.channels {
		tt = append(tt, t)
	}

	return tt
}

// Topic return handlers array subscribe to this topic.
//
// A defensive copy of the internal slice is returned so that callers cannot
// observe (or mutate) the hub's internal state while it is being modified by
// Subscribe/Unsubscribe/Close.
func (h *hub) Topic(topicName topic) ([]*handler, error) {
	h.mtx.RLock()
	defer h.mtx.RUnlock()

	if hs, ok := h.channels[topicName]; ok {
		cp := make([]*handler, len(hs))
		copy(cp, hs)
		return cp, nil
	}

	return nil, fmt.Errorf("topic %s doesn't exist", topicName)
}

// Wait blocks until all in-flight messages have been delivered.
func (h *hub) Wait() {
	h.pendingMtx.Lock()
	for h.pending > 0 {
		h.pendingCond.Wait()
	}
	h.pendingMtx.Unlock()
}

// Close unsubscribe all handlers from given topic
func (h *hub) Close(topicName topic) {
	h.mtx.Lock()
	defer h.mtx.Unlock()

	if _, ok := h.channels[topicName]; ok {
		for _, h := range h.channels[topicName] {
			h.cancel()
		}

		delete(h.channels, topicName)

		return
	}
}

// Destroy unsubscribes all handlers from every topic and stops their
// goroutines. The topic list is snapshotted under the read lock to avoid a
// data race with concurrent Publish/Subscribe calls.
func (h *hub) Destroy() error {
	h.mtx.RLock()
	topics := make([]topic, 0, len(h.channels))
	for t := range h.channels {
		topics = append(topics, t)
	}
	h.mtx.RUnlock()

	for _, t := range topics {
		h.Close(t)
	}

	return nil
}

func buildHandlerArgs(args []interface{}) []reflect.Value {
	reflectedArgs := make([]reflect.Value, 0)

	for _, arg := range args {
		reflectedArgs = append(reflectedArgs, reflect.ValueOf(arg))
	}

	return reflectedArgs
}

// Get return existing instance of Hub or create it and return
func Get() MessageHub {
	instanceMtx.Lock()
	defer instanceMtx.Unlock()

	if instance == nil {
		instance = New()
	}
	return instance
}

// New create and return new instance of Hub
func New() MessageHub {
	h := &hub{channels: make(channelsMap)}
	h.pendingCond = sync.NewCond(&h.pendingMtx)
	h.SetConfig(appmod.NewConfig(`Hub`, `v1.0.0`))
	return h
}

// Sub subscribe listeners
func Sub(topicName string, fn interface{}) error {
	return Get().Subscribe(topic(topicName), fn)
}

// Event dispatch event
func Event(topicName string, args ...interface{}) {
	Get().Publish(topic(topicName), args...)
}

// Reset instance
func Reset() MessageHub {
	_ = Get().Destroy()

	instanceMtx.Lock()
	instance = nil
	instanceMtx.Unlock()

	return Get()
}
