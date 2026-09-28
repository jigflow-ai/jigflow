# Builds jigflow as a single static executable, with `jfl` as its alias.
BIN := bin

.PHONY: build test

build:
	CGO_ENABLED=0 go build -trimpath -o $(BIN)/jigflow ./cmd/jigflow
	ln -sf jigflow $(BIN)/jfl

test:
	go vet ./...
	go test ./...
