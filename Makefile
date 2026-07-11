# Rewind — Makefile
# Targets are designed to be composable and CI-friendly.
# Requires: Go 1.23+, golangci-lint, goreleaser (for release).

BINARY      := rewind
MODULE      := github.com/rewind-io/rewind
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS     := -s -w -X $(MODULE)/internal/cli.rewindVersion=$(VERSION)
BUILD_FLAGS := -trimpath -ldflags "$(LDFLAGS)"

# CGO disabled — single static binary, cross-compile friendly.
export CGO_ENABLED=0

.PHONY: all build test lint vet tidy clean install coverage help

all: build

## build: compile the binary to ./bin/rewind
build:
	@mkdir -p bin
	go build $(BUILD_FLAGS) -o bin/$(BINARY) ./cmd/rewind

## install: install to $GOPATH/bin
install:
	go install $(BUILD_FLAGS) ./cmd/rewind

## test: run all unit tests
test:
	go test -count=1 -timeout 120s ./...

## test-v: run tests with verbose output
test-v:
	go test -count=1 -timeout 120s -v ./...

## coverage: generate coverage report (HTML)
coverage:
	go test -count=1 -timeout 120s -coverprofile=coverage.out \
		-coverpkg=./internal/model/...,./internal/analyze/...,./internal/bundle/... \
		./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## lint: run golangci-lint
lint:
	golangci-lint run ./...

## vet: run go vet
vet:
	go vet ./...

## tidy: tidy and verify modules
tidy:
	go mod tidy
	go mod verify

## clean: remove build artifacts
clean:
	rm -rf bin/ coverage.out coverage.html dist/

## help: print this message
help:
	@grep -E '^## ' Makefile | sed 's/## /  /'
