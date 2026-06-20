SHELL   := /bin/bash
BINARY  := mimo-ss-proxy
PKG     := ./cmd/$(BINARY)
COVER   := coverage.out
MODE    ?= auto
PORT    ?= 18084
FAST_CHECK ?= true

.PHONY: all build build-win test test-race vet lint clean run help install-service enable-service stop-service disable-service uninstall-service service-status service-logs rebuild-service

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
	./$(BINARY) --mode=$(MODE) --port=$(PORT) --fast-check=$(FAST_CHECK)

install-service:
	bash scripts/install-service.sh

enable-service:
	systemctl --user enable $(BINARY)
	systemctl --user start $(BINARY)

stop-service:
	-systemctl --user stop $(BINARY)

disable-service:
	systemctl --user stop $(BINARY)
	systemctl --user disable $(BINARY)

uninstall-service:
	-systemctl --user stop $(BINARY)
	-systemctl --user disable $(BINARY)
	rm -f ~/.config/systemd/user/$(BINARY).service
	systemctl --user daemon-reload

service-status:
	systemctl --user status $(BINARY)

service-logs:
	journalctl --user -u $(BINARY) -f

rebuild-service: stop-service build install-service enable-service

help:
	@echo "Usage:"
	@echo "  make build            Build binary"
	@echo "  make build-win        Build Windows exe (amd64)"
	@echo "  make test             Run all tests"
	@echo "  make test-race        Run tests with race detector"
	@echo "  make vet              Run go vet"
	@echo "  make cover            Show test coverage"
	@echo "  make clean            Remove binary and artifacts"
	@echo "  make run MODE=auto PORT=18084 FAST_CHECK=true"
	@echo ""
	@echo "Systemd user service:"
	@echo "  make install-service     Install systemd user service"
	@echo "  make enable-service      Enable and start the service"
	@echo "  make stop-service        Stop the service"
	@echo "  make disable-service     Stop and disable the service"
	@echo "  make uninstall-service   Stop, disable, and remove the service"
	@echo "  make rebuild-service     Stop, rebuild, reinstall, restart"
	@echo "  make service-status      Show service status"
	@echo "  make service-logs        Show service logs (follow mode)"
	@echo ""
	@echo "Proxy modes: direct, auto, custom, fallback, fallback-proxy"
	@echo "Fast check:  --fast-check=$(FAST_CHECK) (enable concurrent health checks)"
