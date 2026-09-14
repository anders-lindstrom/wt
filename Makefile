BIN := bin/wt
# The version stamp install.sh uses too, so `wt about` on a make build does
# not say dev.
LDFLAGS := $(shell scripts/ldflags.sh)

.PHONY: build test lint bats check clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/wt

test:
	go test ./...

bats: build
	bats test/

lint:
	golangci-lint run

check: lint test bats

clean:
	rm -rf bin
