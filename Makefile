BIN := bin/wt

.PHONY: build test lint bats check clean

build:
	go build -o $(BIN) ./cmd/wt

test:
	go test ./...

bats: build
	bats test/

lint:
	golangci-lint run

check: lint test bats

clean:
	rm -rf bin
