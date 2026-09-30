EXT := $(if $(filter Windows_NT,$(OS)),.exe,)
BINARY ?= midden$(EXT)

.PHONY: all build test vet fmt check
all: check build

build:
	go build -trimpath -o $(BINARY) ./cmd/midden

test:
	go test ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

check: vet test
