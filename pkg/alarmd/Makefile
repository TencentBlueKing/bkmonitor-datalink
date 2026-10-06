GO ?= go

RELEASE_PATH ?= $(PWD)/dist
VERSION ?= $(shell cat VERSION)
COMMIT_ID ?= $(shell git rev-parse HEAD)
SCHEMA_VERSION ?= $(shell cat SCHEMA_VERSION)
BIN = $(RELEASE_PATH)/alarmd
LDFLAGS = -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT_ID) \
	-X main.schemaVersion=$(SCHEMA_VERSION)

.PHONY: help
help:
	@echo "Make Targets:"
	@echo " verify: Verify module dependencies"
	@echo " test:   Run unit tests"
	@echo " race:   Run unit tests with the race detector"
	@echo " lint:   Check formatting and run static analysis"
	@echo " build:  Build the linux/amd64 binary"
	@echo " proto:  Regenerate the control stream's Go code from viewstream/pb/*.proto"

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
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/alarmd

# The generated code is committed; this target is for a change to the .proto.
# Plugin versions are pinned so two people regenerating produce one diff;
# the protoc release is not, since these options work back to protoc 3.0.
PROTOC ?= protoc
PROTOC_GEN_GO_VERSION ?= v1.32.0
PROTOC_GEN_GO_GRPC_VERSION ?= v1.3.0
.PHONY: proto
proto:
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)
	PATH="$$($(GO) env GOPATH)/bin:$$PATH" $(PROTOC) -I viewstream/pb \
		--go_out=paths=source_relative:viewstream/pb \
		--go-grpc_out=paths=source_relative:viewstream/pb \
		viewstream/pb/alarmd_control_v1.proto
