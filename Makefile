# Midden development shortcuts.
#
# GNU Make is optional. The cross-platform source of truth is documented in
# docs/DEVELOPMENT.md and uses direct Go commands.

BINARY ?= midden
PKG    := ./cmd/midden

.PHONY: all build test vet fmt check clean install start ui

all: check build

build:
	go build -trimpath -o $(BINARY) $(PKG)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

check: fmt vet test

install:
	go install $(PKG)

clean:
	$(RM) $(BINARY) $(BINARY).exe

start: build
	./$(BINARY) start

ui: build
	./$(BINARY) ui
