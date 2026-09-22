PREFIX ?= $(HOME)/.local
.PHONY: build test install
build:
	mkdir -p bin
	go build -buildvcs=false -o bin/coding-worker ./cmd/coding-worker
	go build -buildvcs=false -o bin/workerctl ./cmd/workerctl
test:
	go test -race ./...
install: build
	install -d $(PREFIX)/bin
	install -m 755 bin/coding-worker bin/workerctl $(PREFIX)/bin/
