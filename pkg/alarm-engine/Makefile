GO ?= go

RELEASE_PATH ?= $(PWD)/dist
VERSION ?= $(shell cat VERSION)
COMMIT_ID ?= $(shell git rev-parse HEAD)
SCHEMA_VERSION ?= none
BIN = $(RELEASE_PATH)/alarm-engine
LDFLAGS = -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT_ID) \
	-X main.schemaVersion=$(SCHEMA_VERSION)

.PHONY: help
help:
	@echo "Make Targets:"
	@echo " test:  Run unit tests with the race detector"
	@echo " lint:  Run static analysis"
	@echo " build: Build the linux/amd64 binary"

.PHONY: test
test:
	$(GO) test -race -count=1 ./...

.PHONY: lint
lint:
	$(GO) vet ./...

.PHONY: build
build:
	mkdir -p $(RELEASE_PATH)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/alarm-engine
