BINARY := bin/papermgr
GOBIN ?= $(HOME)/.local/bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
LDFLAGS := -X main.Version=$(VERSION)

.PHONY: build test vet install clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/papermgr

test:
	CGO_ENABLED=0 go test ./...

vet:
	go vet ./...

install:
	GOBIN=$(GOBIN) CGO_ENABLED=0 go install -ldflags "$(LDFLAGS)" ./cmd/papermgr

clean:
	rm -rf bin
