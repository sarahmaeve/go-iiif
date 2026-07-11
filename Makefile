# iiifpreserve — build/dev tasks. Single Go binary, stdlib-first.
BINARY := iiifpreserve
PKG    := ./cmd/iiifpreserve

.PHONY: all build install test browser-test lint fmt vet tidy clean viewer

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

## browser-test: parked experimental Chrome smoke harness; skipped unless
## IIIF_BROWSER_SMOKE=1 is explicitly set (and CHROME_BIN if needed).
browser-test:
	go test -tags=browser ./internal/serve -run BrowserSmoke -count=1

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

## viewer: rebuild the vendored Mirador 4 + MAE UMD bundle (needs Node;
## one-time vendoring step — the iiifpreserve binary itself never needs
## Node). MAE requires the *latest* Mirador 4 (its companion-window render
## path landed after the 4.0.0 npm tag), so the bundle is built against a
## local Mirador source checkout, not the npm release. Point MIRADOR_SRC at
## a Mirador 4 clone (default: sibling ../mirador). Output is committed at
## internal/serve/viewer/mirador.min.js.
MIRADOR_SRC ?= ../mirador
viewer:
	cd $(MIRADOR_SRC) && npm ci && npm run build
	cd viewer-src && npm ci && npm run build
	cp viewer-src/dist/mirador.min.js internal/serve/viewer/mirador.min.js

## clean: remove build artifacts
clean:
	rm -rf bin viewer-src/dist viewer-src/node_modules
