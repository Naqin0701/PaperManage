BINARY := bin/papermgr

.PHONY: build test vet install demo clean

build:
	CGO_ENABLED=0 go build -o $(BINARY) ./cmd/papermgr

test:
	CGO_ENABLED=0 go test ./...

vet:
	go vet ./...

install:
	CGO_ENABLED=0 go install ./cmd/papermgr

demo:
	./scripts/demo.sh

clean:
	rm -rf bin
