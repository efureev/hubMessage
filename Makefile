PKG := ./...

.DEFAULT_GOAL := test

## test: run tests with race detector and coverage profile
test:
	go test -race -covermode=atomic -coverprofile=coverage.out $(PKG)

## cover: open the HTML coverage report (runs tests first)
cover: test
	go tool cover -html=coverage.out

## check: run all static checks (vet + lint)
check: vet lint

## vet: run go vet
vet:
	go vet $(PKG)

## lint: run golangci-lint
lint:
	golangci-lint run $(PKG)

## download-tools: install development tools
download-tools:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

## tidy: tidy go modules
tidy:
	go mod tidy

## clean: remove generated artifacts
clean:
	rm -f coverage.out

.PHONY: test cover check vet lint download-tools tidy clean
