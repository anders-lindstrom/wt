BIN := bin/wt
# The version stamp install.sh uses too, so `wt about` on a make build does
# not say dev.
LDFLAGS := $(shell scripts/ldflags.sh)

.PHONY: build test test-fast test-unit lint bats check clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/wt

test:
	go test -timeout 30m ./...

# The suite without the tests that guard on testing.Short(): docs/testing.md.
test-fast:
	go test -short -timeout 20m ./...

# The packages whose tests build no git repository.
UNIT := ./internal/config ./internal/find ./internal/naming ./internal/about \
	./internal/quarantine ./internal/gittest ./internal/superset ./schema

test-unit:
	go test -short -timeout 5m $(UNIT)

# scripts/bats.sh kills a hung run after 10 minutes; BATS_DEADLINE changes it.
bats: build
	scripts/bats.sh test/

lint:
	golangci-lint run

check: lint test bats

clean:
	rm -rf bin
