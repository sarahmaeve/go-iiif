# iiifpreserve — build/dev tasks. Single Go binary, stdlib-first.
BINARY := iiifpreserve
PKG    := ./cmd/iiifpreserve

.PHONY: all build install test lint fmt vet tidy clean

## all: format, vet, lint, test, build (the pre-commit gate plus build)
all: fmt vet lint test build

## build: compile the binary into ./bin
build:
	go build -o bin/$(BINARY) $(PKG)

## install: install the binary into $(go env GOBIN) (or $GOPATH/bin)
install:
	go install $(PKG)

## test: run the full offline test suite
test:
	go test ./...

## lint: run golangci-lint across the module
lint:
	golangci-lint run ./...

## fmt: gofmt-format all source
fmt:
	gofmt -w cmd internal

## vet: go vet across the module
vet:
	go vet ./...

## tidy: prune and verify go.mod/go.sum
tidy:
	go mod tidy

## clean: remove build artifacts
clean:
	rm -rf bin
