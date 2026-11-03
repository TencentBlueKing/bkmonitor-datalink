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
	@echo " build:  Build the linux/amd64 binary with the alarmd-cli release embedded"
	@echo " proto:  Regenerate the control stream's Go code from viewstream/pb/*.proto"

.PHONY: verify
verify:
	$(GO) mod verify
	$(GO) mod tidy -diff

# cmd/alarmd is the longest package: about 8.8 minutes plain and 12 under
# -race on a developer machine, the latter past go test's 10 minute
# default. The timeouts leave it twice that and more.
TEST_TIMEOUT ?= 20m
RACE_TIMEOUT ?= 30m

.PHONY: test
test:
	$(GO) test -count=1 -timeout $(TEST_TIMEOUT) ./...

.PHONY: race
race:
	$(GO) test -race -count=1 -timeout $(RACE_TIMEOUT) ./...

.PHONY: lint
lint:
	test -z "$$(gofmt -l .)"
	$(GO) vet ./...

# The alarmd-cli release is packed from ../alarmd-cli with the same version
# and embedded in the binary (clirelease), so whatever copies the binary ships
# the CLI too. The release needs a release version: the root Makefile passes
# 0.4.<BUILD_NO>; a plain local make, whose VERSION is the 0.4.x line, builds
# without it and says so. CLI_RELEASE=0 also builds without it. Building
# without it clears what an earlier pack left, which go:embed would otherwise
# pick up, and the distribution routes then answer cli_release_unavailable.
# On a linux/amd64 host the built binary checks its own release.
CLI_SOURCE ?= ../alarmd-cli
ifeq ($(shell echo '$(VERSION)' | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$$' && echo yes),yes)
CLI_RELEASE ?= 1
else
CLI_RELEASE ?= 0
CLI_RELEASE_NOTICE = alarmd-cli release skipped: VERSION $(VERSION) is not a release version (set VERSION=<x.y.z> to embed one)
endif

.PHONY: build
build:
	mkdir -p $(RELEASE_PATH)
ifneq ($(CLI_RELEASE),0)
	$(GO) run ./clirelease/pack -cli $(CLI_SOURCE) -version $(VERSION) -out clirelease/assets
else
	$(if $(CLI_RELEASE_NOTICE),@echo "$(CLI_RELEASE_NOTICE)")
	find clirelease/assets -mindepth 1 -maxdepth 1 ! -name README.md -exec rm -rf {} +
endif
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/alarmd
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	$(GO) build -trimpath -ldflags "-s -w" -o $(RELEASE_PATH)/alarmd-node-runner ./cmd/alarmd-node-runner
ifneq ($(CLI_RELEASE),0)
	if [ "$$($(GO) env GOHOSTOS)/$$($(GO) env GOHOSTARCH)" = linux/amd64 ]; then $(BIN) --check-cli-release; fi
endif

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
