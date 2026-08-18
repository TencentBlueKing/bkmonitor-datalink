GO ?= go

RELEASE_PATH ?= $(PWD)/dist
VERSION ?= $(shell cat VERSION)
COMMIT_ID ?= $(shell git rev-parse HEAD)
SCHEMA_VERSION ?= $(shell cat SCHEMA_VERSION)
COMPARATOR_SCHEMA_VERSION ?= comparison-audit-batch/1.0
BIN = $(RELEASE_PATH)/alarm-engine
COMPARATOR_BIN = $(RELEASE_PATH)/alarm-engine-comparator
LDFLAGS = -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT_ID) \
	-X main.schemaVersion=$(SCHEMA_VERSION)
COMPARATOR_LDFLAGS = -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT_ID) \
	-X main.schemaVersion=$(COMPARATOR_SCHEMA_VERSION)

.PHONY: help
help:
	@echo "Make Targets:"
	@echo " verify: Verify module dependencies"
	@echo " test:   Run unit tests"
	@echo " race:   Run unit tests with the race detector"
	@echo " lint:   Check formatting and run static analysis"
	@echo " build:  Build the linux/amd64 binary"

.PHONY: verify
verify:
	$(GO) mod verify
	$(GO) mod tidy -diff

.PHONY: test
test:
	$(GO) test -count=1 ./...

.PHONY: race
race:
	$(GO) test -race -count=1 ./...

.PHONY: lint
lint:
	test -z "$$(gofmt -l .)"
	$(GO) vet ./...

.PHONY: build
build:
	mkdir -p $(RELEASE_PATH)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/alarm-engine
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	$(GO) build -trimpath -ldflags "$(COMPARATOR_LDFLAGS)" -o $(COMPARATOR_BIN) ./cmd/alarm-engine-comparator
