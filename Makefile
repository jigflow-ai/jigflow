# Builds jigflow as a single static executable, with `jfl` as its alias, and
# the reference GitHub Issues and Linear Connectors alongside it.
BIN := bin

.PHONY: build test

build:
	CGO_ENABLED=0 go build -trimpath -o $(BIN)/jigflow ./cmd/jigflow
	ln -sf jigflow $(BIN)/jfl
	CGO_ENABLED=0 go build -trimpath -o $(BIN)/jfl-connector-github ./cmd/jfl-connector-github
	CGO_ENABLED=0 go build -trimpath -o $(BIN)/jfl-connector-linear ./cmd/jfl-connector-linear

test:
	go vet ./...
	go test ./...
