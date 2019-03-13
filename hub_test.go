package hub

import (
	"errors"
	"fmt"
	. "github.com/efureev/appmod"
	"github.com/smartystreets/goconvey/convey"
	"sync"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	h := Reset()

	if h == nil {
		t.Fail()
	}
}

func TestGet(t *testing.T) {
	h := Reset()

	if h == nil {
		t.Fail()
	}
}

func TestSubscribe(t *testing.T) {
	h := Reset()

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
	h := Reset()

	var wg sync.WaitGroup
	wg.Add(3)

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

	str := ``
	h.Subscribe("test", func(v string) {
		defer wg.Done()
		str = v
	})

	h.Publish("topic", true)
	h.Publish("test", `2`)

	wg.Wait()

	if len(h.Topics()) != 2 {
		t.Fail()
	}

	if str != `2` {
		t.Fail()
	}

	if first == false || second == false {
		t.Fail()
	}
}

func worker(wg *sync.WaitGroup, poll *sync.Map, i int) {

	for j := 0; j < 5; j++ {
		go func(j int) {
			defer wg.Done()
			//log.Printf("[%d] worker [%d]\n", i, j)
			Event(`topic`, poll, i, j)
		}(j)
	}

}

func TestPublishAsync(t *testing.T) {
	h := Reset()

	var wg sync.WaitGroup
	var counters sync.Map
	wg.Add(20)

	h.Subscribe("topic", func(poll *sync.Map, i, j int) {
		//log.Printf("event [%d][%d]\n", i, j)

		v, ok := poll.Load(i)
		if !ok {
			var p sync.Map
			p.Store(j, true)
			poll.Store(i, &p)
		} else {
			if val, ok := v.(*sync.Map); ok {
				(*val).Store(j, true)
				poll.Store(i, &v)
			}
		}

		/*if poll[i] == nil {
			poll[i] = make(map[int]bool)
		}
		poll[i][j] = true*/
	})

	for i := 0; i < 4; i++ {
		go worker(&wg, &counters, i)
	}

	wg.Wait()

	length := 0

	counters.Range(func(_, v interface{}) bool {
		length++

		l := 0

		if val, ok := v.(*sync.Map); ok {
			val.Range(func(_, _ interface{}) bool {
				l++

				return true
			})

			if l != 5 {
				t.Fail()
			}
		}

		return true
	})
}

func workerPackage(wg *sync.WaitGroup, fireChan chan bool, poll *sync.Map, i int) {

	t := topic(fmt.Sprintf(`topic.%d`, i))

	Get().Subscribe(t, func(poll *sync.Map, fc chan bool, i, j int) {
		//println(`e`, i, j)

		v, ok := poll.Load(i)
		if !ok {
			var p sync.Map
			p.Store(j, true)
			poll.Store(i, p)
		} else {
			if val, ok := v.(sync.Map); ok {
				val.Store(j, true)
				poll.Store(i, val)
			}
		}

		fc <- true
	})

	for j := 0; j < 5; j++ {
		go func(j int) {
			defer wg.Done()

			Event(t, poll, fireChan, i, j)
			Event(t, poll, fireChan, i, j)
			Event(t, poll, fireChan, i, j)
		}(j)
	}

}

func TestPublishAsyncFromAny(t *testing.T) {
	Reset()

	var wg sync.WaitGroup
	var counters sync.Map
	fireCount := 0
	fireCountChan := make(chan bool)

	go func() {
		for {
			_, more := <-fireCountChan
			if more {
				fireCount++
			}
		}

		/*for range fireCountChan {
			fireCount++
		}*/
	}()

	wg.Add(10)

	for i := 0; i < 2; i++ {
		go workerPackage(&wg, fireCountChan, &counters, i)
	}

	wg.Wait()
	time.Sleep(500 * time.Millisecond)

	length := 0

	counters.Range(func(k, v interface{}) bool {
		length++

		l := 0

		val, ok := v.(sync.Map)
		if ok {
			val.Range(func(_, _ interface{}) bool {
				l++

				return true
			})

			if l != 5 {
				t.Fail()
			}
		}

		return true
	})

	if length != 2 {
		t.Fail()
	}

	if fireCount != 30 {
		t.Fail()
	}

}

func TestTopic(t *testing.T) {
	h := Reset()

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

	hh, err := h.Topic(`topic`)

	if err != nil {
		t.Fail()
	}

	if len(hh) != 2 {
		t.Fail()
	}

	hh, err = h.Topic(`topic2`)
	if hh != nil {
		t.Fail()
	}
	if err == nil {
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
