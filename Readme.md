# hubMessage

[![Test](https://github.com/efureev/hubMessage/actions/workflows/test.yml/badge.svg)](https://github.com/efureev/hubMessage/actions/workflows/test.yml)
[![Codacy Badge](https://api.codacy.com/project/badge/Grade/0cdced379f3e41d39732a720263c8393)](https://app.codacy.com/app/efureev/hubMessage?utm_source=github.com&utm_medium=referral&utm_content=efureev/hubMessage&utm_campaign=Badge_Grade_Dashboard)
[![Maintainability](https://api.codeclimate.com/v1/badges/82d6074b251f785f8c23/maintainability)](https://codeclimate.com/github/efureev/hubMessage/maintainability)
[![Test Coverage](https://api.codeclimate.com/v1/badges/82d6074b251f785f8c23/test_coverage)](https://codeclimate.com/github/efureev/hubMessage/test_coverage)
[![codecov](https://codecov.io/gh/efureev/hubMessage/branch/master/graph/badge.svg)](https://codecov.io/gh/efureev/hubMessage)
[![Go Report Card](https://goreportcard.com/badge/github.com/efureev/hubMessage)](https://goreportcard.com/report/github.com/efureev/hubMessage)

`hubMessage` is a lightweight in-process **publish/subscribe (event bus)** library for Go.

It lets different parts of an application communicate through named **topics** without
direct dependencies between them: producers `Publish` messages to a topic, and any number
of subscribers registered via `Subscribe` receive them asynchronously.

### Features

- Simple publish/subscribe API built around named topics.
- Handlers are plain functions with arbitrary signatures — arguments passed to `Publish`
  are delivered to the subscriber via reflection.
- Each subscriber runs in its own goroutine; publishing is non-blocking for the producer.
- `Wait()` lets you block until all in-flight messages have been delivered.
- A package-level singleton (`Get`, `Sub`, `Event`, `Reset`) for app-wide event bus usage.
- Integrates with [`appmod`](https://github.com/efureev/appmod) as an application module
  (lifecycle hooks like `BeforeStart`, `Init`, `Destroy`).

### Requirements

- Go 1.13+

### Install

```bash
go get -u github.com/efureev/hubMessage
```

> The module path is `github.com/efureev/hubMessage`, the package name is `hub`.

### API overview

| Function / Method                                  | Description                                                        |
|----------------------------------------------------|--------------------------------------------------------------------|
| `hub.New() MessageHub`                             | Create a new, independent hub instance.                            |
| `hub.Get() MessageHub`                             | Return the shared (singleton) hub, creating it on first call.      |
| `hub.Reset() MessageHub`                           | Destroy the shared hub and create a fresh one.                     |
| `hub.Sub(topic string, fn interface{}) error`      | Subscribe `fn` to a topic on the shared hub.                       |
| `hub.Event(topic string, args ...interface{})`     | Publish a message to a topic on the shared hub.                    |
| `(h) Subscribe(topic, fn) error`                   | Register a handler function for a topic.                           |
| `(h) Unsubscribe(topic, fn) error`                 | Remove a previously registered handler.                            |
| `(h) Publish(topic, args...)`                      | Deliver `args` to every handler subscribed to the topic.           |
| `(h) Topics() []topic`                             | List all topics that currently have subscribers.                   |
| `(h) Topic(topic) ([]*handler, error)`             | Return the handlers registered for a topic.                        |
| `(h) Close(topic)`                                 | Unsubscribe all handlers from a topic.                             |
| `(h) Wait()`                                       | Block until all published messages have been processed.            |

> Handler signatures must match the arguments passed to `Publish`/`Event`; a mismatch
> will panic at delivery time (reflection `Call`).

## Examples
### Basic
```go
import (
	"github.com/efureev/hubMessage"
)

func main() {
	h := hub.New()
    defer h.Wait()
	
    h.Subscribe("console", func(msg string) {
        println(msg)
    })
    
	//..
    
    h.Publish("console", `Hi`)
    hub.Event("console", `test msg`)
	//...
}
```

```go
package main

import (
	"github.com/efureev/appmod"
	"github.com/efureev/hubMessage"
	"log"
)

func main() {
    hub.Get().BeforeStart(func(_ appmod.AppModule) error {
        hub.Sub(`app.console`, func(msg string) {
            log.Println(msg)
        })
    
        return nil
    })
    defer hub.Get().Wait() // if you want wait for finish message sending
    hub.Get().Init()
    
    // ... send message to hub from any places
    
    hub.Event(`app.console`, `Config loaded`)
    hub.Event(`app.console`, `Test message`)
}
```

### Error handling
```go
package main

import (
	"errors"
	"github.com/efureev/hubMessage"
	"log"
)

func main() {
	h := hub.New()
    out := make(chan error)
    fatal := make(chan error)
    defer h.Wait()
    defer close(out)
    defer close(fatal)
    
    go func() {
    	for {
            select{
            case e:= <-out:
                println(e)
            case e:= <-fatal:
                log.Fatal(e)
            }
    	}
    }()
    
    h.Subscribe("errors", func(err error) {
        out <- err
    })
    
    h.Subscribe("errors.fatal", func(err error) {
        fatal <- err
    })
    
    h.Subscribe("errors.toChannel", func(err error, ch chan <- error) {
        ch <- err
    })

    
    h.Publish("errors", errors.New("I do throw error"))
    h.Publish("errors.fatal", errors.New("I do throw error"))
    h.Publish("errors.toChannel", errors.New("I do throw error"), fatal)
    h.Publish("errors.toChannel", errors.New("I do throw error"), out)
}
```


### Event bus
```go

import (
	"auth/internal/models"
	hub "github.com/efureev/hubMessage"
)

func registerEvents(events map[string]interface{}) {
	for event, handle := range events {
		err := hub.Sub(event, handle)
		if err != nil {
			panic(err)
		}
	}

}

func eventList() map[string]interface{} {
	return map[string]interface{}{
		`user.registered`: func(user *models.User) {
			println(`user registered: ` + user.Id)
		},
		`user.activated`: func(user *models.User) {
			println(`user activated: ` + user.Id)
		},
		`test`: func(_ string) {
            out <- `test`
        },
        `empty`: func() {
            out <- `empty`
        },
	}
}

// ... in other code:
hub.Event(`user.registered`, &models.User{})
hub.Event(`empty`)

```
