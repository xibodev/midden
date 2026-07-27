# midden — M0
#
# Deterministic, read-only session mapping across Copilot CLI, Claude Code and
# opencode. No LLM calls anywhere in this milestone.

BINARY := midden
PKG    := ./cmd/midden

.PHONY: all build test vet fmt check clean install run-doctor

all: check build

build:
	go build -o $(BINARY) $(PKG)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

check: fmt vet test

install:
	go install $(PKG)

clean:
	rm -f $(BINARY) $(BINARY).exe

run-doctor: build
	./$(BINARY) doctor
