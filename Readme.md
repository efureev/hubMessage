# EventBus System
[![Codacy Badge](https://api.codacy.com/project/badge/Grade/0cdced379f3e41d39732a720263c8393)](https://app.codacy.com/app/efureev/hubMessage?utm_source=github.com&utm_medium=referral&utm_content=efureev/hubMessage&utm_campaign=Badge_Grade_Dashboard)
[![Build Status](https://travis-ci.org/efureev/hubMessage.svg?branch=master)](https://travis-ci.org/efureev/hubMessage)
[![Maintainability](https://api.codeclimate.com/v1/badges/82d6074b251f785f8c23/maintainability)](https://codeclimate.com/github/efureev/hubMessage/maintainability)
[![Test Coverage](https://api.codeclimate.com/v1/badges/82d6074b251f785f8c23/test_coverage)](https://codeclimate.com/github/efureev/hubMessage/test_coverage)
[![codecov](https://codecov.io/gh/efureev/hubMessage/branch/master/graph/badge.svg)](https://codecov.io/gh/efureev/hubMessage)
[![Go Report Card](https://goreportcard.com/badge/github.com/efureev/hubMessage)](https://goreportcard.com/report/github.com/efureev/hubMessage)

# Install
```bash
go get -u github.com/efureev/hubMessage
```

# Examples
### Basic
```go
import (
	"errors"
	"github.com/efureev/hubMessage"
	"log"
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
