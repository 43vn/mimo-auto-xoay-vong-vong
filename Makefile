SHELL   := /bin/bash
BINARY  := mimo-ss-proxy
PKG     := ./cmd/$(BINARY)
COVER   := coverage.out

.PHONY: all build build-win test test-race vet lint clean run help

all: build test

build:
	go build -o $(BINARY) $(PKG)

build-win:
	GOOS=windows GOARCH=amd64 go build -o $(BINARY).exe $(PKG)

test:
	go test ./... -count=1

test-race:
	go test ./... -race -count=1

vet:
	go vet ./...

lint: vet

cover:
	go test ./... -coverprofile=$(COVER) -covermode=atomic
	go tool cover -func=$(COVER)
	@rm -f $(COVER)

clean:
	rm -f $(BINARY) $(BINARY).exe $(COVER)
	go clean

run: build
	./$(BINARY) --mode=$(MODE) --port=$(PORT)

help:
	@echo "Usage:"
	@echo "  make build        Build binary"
	@echo "  make build-win    Build Windows exe (amd64)"
	@echo "  make test         Run all tests"
	@echo "  make test-race    Run tests with race detector"
	@echo "  make vet          Run go vet"
	@echo "  make cover        Show test coverage"
	@echo "  make clean        Remove binary and artifacts"
	@echo "  make run MODE=direct PORT=18084"
	@echo ""
	@echo "Proxy modes: direct, auto, custom, fallback, fallback-proxy"
