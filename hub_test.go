package hub

import (
	"errors"
	. "github.com/efureev/appmod"
	"github.com/smartystreets/goconvey/convey"
	"sync"
	"testing"
)

func TestNew(t *testing.T) {
	h := New()

	if h == nil {
		t.Fail()
	}
}

func TestGet(t *testing.T) {
	h := Get()

	if h == nil {
		t.Fail()
	}
}

func TestSubscribe(t *testing.T) {
	h := Get()

	handler := func() {}

	if h.Subscribe("test", handler) != nil {
		t.Fail()
	}

	if h.Subscribe("test", 2) == nil {
		t.Fail()
	}

	if h.Subscribe("test", handler) != nil {
		t.Fail()
	}
}

func TestPublish(t *testing.T) {
	h := New()

	var wg sync.WaitGroup
	wg.Add(2)

	first := false
	second := false

	h.Subscribe("topic", func(v bool) {
		defer wg.Done()
		first = v
	})

	h.Subscribe("topic", func(v bool) {
		defer wg.Done()
		second = v
	})

	h.Publish("topic", true)

	wg.Wait()

	if first == false || second == false {
		t.Fail()
	}
}

func TestHandleError(t *testing.T) {
	h := New()

	h.Subscribe("topic", func(out chan<- error) {
		out <- errors.New("I do throw error")
	})

	out := make(chan error)
	defer close(out)

	h.Publish("topic", out)

	if <-out == nil {
		t.Fail()
	}
}

func TestGlobalFunc(t *testing.T) {
	out := make(chan string)
	defer close(out)

	Sub(`console`, func(m string) {
		out <- m
	})

	Event(`console`, `test`)

	if <-out == `` {
		t.Fail()
	}

	if err := Sub(`console`, 1); err == nil {
		t.Fail()
	}

}

func TestHub(t *testing.T) {
	convey.Convey("Hub", t, func() {
		h := Reset()

		convey.Convey("Base", func() {
			convey.So(h, convey.ShouldHaveSameTypeAs, &hub{})
		})

		convey.Convey("Config", func() {
			convey.So(h.Config(), convey.ShouldHaveSameTypeAs, Config{})
			convey.So(h.Config().Name(), convey.ShouldEqual, `Hub`)
			convey.So(h.Config().Version(), convey.ShouldEqual, `v1.0.0`)
		})

		convey.Convey("Unsubscribe", func() {
			handler := func() {}

			if h.Subscribe("test", handler) != nil {
				t.Fail()
			}

			err := h.Unsubscribe("test", handler)
			convey.So(err, convey.ShouldBeNil)

			err = h.Unsubscribe("test", handler)
			convey.So(err, convey.ShouldBeNil)

			err = h.Unsubscribe("unexisted", handler)
			convey.So(err, convey.ShouldBeError)

		})

		convey.Convey("Close", func() {
			handler := func() {}

			if h.Subscribe("test", handler) != nil {
				t.Fail()
			}

			original, ok := h.(*hub)

			convey.Convey("Cast message bus to its original type", func() {
				convey.So(ok, convey.ShouldBeTrue)
			})

			convey.Convey("Subscribed handler to topic", func() {
				convey.So(len(original.channels), convey.ShouldEqual, 1)
			})

			h.Close("test")

			convey.Convey("Unsubscribed handlers from topic", func() {
				convey.So(len(original.channels), convey.ShouldBeZeroValue)
			})

		})

		convey.Convey("Destroy", func() {
			handler := func() {}

			if h.Subscribe("test", handler) != nil {
				t.Fail()
			}

			if h.Subscribe("console", handler) != nil {
				t.Fail()
			}

			if h.Subscribe("console", func() {}) != nil {
				t.Fail()
			}

			convey.So(h.Destroy(), convey.ShouldBeNil)
			original := h.(*hub)
			convey.So(len(original.channels), convey.ShouldBeZeroValue)

		})

	})
}
