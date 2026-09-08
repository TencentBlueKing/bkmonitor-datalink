SHELL := /bin/sh

GO ?= go
GOFMT ?= gofmt
GOLANGCI_LINT ?= golangci-lint
DEVTOOLS_DIR ?= devtools
IMAGE ?= linkd
IMAGE_TAG ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
VERSION ?= $(IMAGE_TAG)
GO_IMAGE ?= golang:1.26.7
GOPROXY ?= direct
CONTAINER ?= $(shell command -v docker >/dev/null 2>&1 && echo docker || echo container)

GO_FILES := $(shell find . -type f -name '*.go' \
	-not -path './vendor/*' \
	-not -path './devtools/node_modules/*' \
	-not -path './devtools/dist/*' \
	-not -path './devtools/dist-server/*' | LC_ALL=C sort)

.PHONY: fmt fmt-check test race vet lint devtools-check devtools-e2e check image

fmt:
	$(GOFMT) -w $(GO_FILES)
	cd $(DEVTOOLS_DIR) && ./node_modules/.bin/prettier --write .

fmt-check:
	@unformatted="$$( $(GOFMT) -l $(GO_FILES) )"; \
	if [ -n "$$unformatted" ]; then \
		echo "以下 Go 文件需要执行 gofmt:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	cd $(DEVTOOLS_DIR) && ./node_modules/.bin/prettier --check .

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

devtools-check:
	cd $(DEVTOOLS_DIR) && ./node_modules/.bin/eslint . --max-warnings 0
	cd $(DEVTOOLS_DIR) && ./node_modules/.bin/tsc -p tsconfig.web.json --noEmit
	cd $(DEVTOOLS_DIR) && ./node_modules/.bin/tsc -p tsconfig.server.json --noEmit
	cd $(DEVTOOLS_DIR) && ./node_modules/.bin/vitest run
	cd $(DEVTOOLS_DIR) && ./node_modules/.bin/vite build
	cd $(DEVTOOLS_DIR) && ./node_modules/.bin/tsc -p tsconfig.server.json

devtools-e2e:
	cd $(DEVTOOLS_DIR) && ./node_modules/.bin/playwright test

check:
	$(MAKE) fmt-check
	$(MAKE) test
	$(MAKE) vet
	$(MAKE) race
	$(MAKE) lint
	$(MAKE) devtools-check

image:
	$(CONTAINER) build \
		--build-arg GO_IMAGE=$(GO_IMAGE) \
		--build-arg GOPROXY=$(GOPROXY) \
		--build-arg VERSION=$(VERSION) \
		-t $(IMAGE):$(IMAGE_TAG) \
		.
