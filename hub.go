package hub

import (
	"context"
	"fmt"
	"github.com/efureev/appmod"
	"reflect"
	"sync"
)

// MessageHub implements publish/subscribe messaging paradigm
type MessageHub interface {
	appmod.AppModule

	Publish(topicName topic, args ...interface{})
	Close(topicName topic)
	Subscribe(topicName topic, fn interface{}) error
	Unsubscribe(topicName topic, fn interface{}) error
}

type hub struct {
	appmod.BaseAppModule

	mtx      sync.RWMutex
	channels channelsMap
}

var instance MessageHub

type topic string
type channelsMap map[topic][]*handler

type handler struct {
	ctx      context.Context
	callback reflect.Value
	cancel   context.CancelFunc
	queue    chan []reflect.Value
}

// Publish publishes arguments to the given topic subscribers
func (h *hub) Publish(topicName topic, args ...interface{}) {
	rArgs := buildHandlerArgs(args)

	h.mtx.RLock()
	defer h.mtx.RUnlock()

	if hs, ok := h.channels[topicName]; ok {
		for _, h := range hs {
			h.queue <- rArgs
		}
	}
}

// Subscribe subscribes to the given topic
func (h *hub) Subscribe(topicName topic, fn interface{}) error {
	if reflect.TypeOf(fn).Kind() != reflect.Func {
		return fmt.Errorf("%s is not a reflect.Func", reflect.TypeOf(fn))
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
				if ok {
					hndr.callback.Call(args)
				}
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

// Unsubscribe unsubscribe handler from the given topic
func (h *hub) Unsubscribe(topicName topic, fn interface{}) error {
	h.mtx.Lock()
	defer h.mtx.Unlock()

	if _, ok := h.channels[topicName]; ok {
		rv := reflect.ValueOf(fn)

		for i, ch := range h.channels[topicName] {
			if ch.callback == rv {
				ch.cancel()
				close(ch.queue)
				h.channels[topicName] = append(h.channels[topicName][:i], h.channels[topicName][i+1:]...)
			}
		}

		return nil
	}

	return fmt.Errorf("topic %s doesn't exist", topicName)
}

// Close unsubscribe all handlers from given topic
func (h *hub) Close(topicName topic) {
	h.mtx.Lock()
	defer h.mtx.Unlock()

	if _, ok := h.channels[topicName]; ok {
		for _, h := range h.channels[topicName] {
			h.cancel()
			close(h.queue)
		}

		delete(h.channels, topicName)

		return
	}
}

func (h *hub) Destroy() error {
	for t := range h.channels {
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
	if instance == nil {
		instance = New()
	}
	return instance
}

// New create and return new instance of Hub
func New() MessageHub {
	h := &hub{channels: make(channelsMap)}
	h.SetConfig(appmod.NewConfig(`Hub`, `v1.0.0`))
	return h
}
