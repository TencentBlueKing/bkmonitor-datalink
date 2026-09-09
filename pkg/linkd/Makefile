SHELL := /bin/sh

GO ?= go
GOFMT ?= gofmt
GOLANGCI_LINT ?= golangci-lint
CONSOLE_DIR ?= console
IMAGE ?= linkd
IMAGE_TAG ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
VERSION ?= $(IMAGE_TAG)
GIT_COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
GO_IMAGE ?= golang:1.26.7
GOPROXY ?= direct
CONTAINER ?= $(shell command -v docker >/dev/null 2>&1 && echo docker || echo container)
CONSOLE_IMAGE ?= linkd-console
NODE_IMAGE ?= node:24-bookworm-slim
NODE_BUILD_OPTIONS ?= --max-old-space-size=3072
CONSOLE_BUILD_FLAGS ?= --memory 4G
EVENTGEN_IMAGE ?= linkd-eventgen
EVENTGEN_VERSION ?= 0.1.0

GO_FILES := $(shell find . -type f -name '*.go' \
	-not -path './vendor/*' \
	-not -path './console/node_modules/*' \
	-not -path './console/dist/*' \
	-not -path './console/dist-server/*' | LC_ALL=C sort)

.PHONY: fmt fmt-check test race vet lint console-check console-e2e helm-check release-check check image console-image eventgen-image

fmt:
	$(GOFMT) -w $(GO_FILES)
	cd $(CONSOLE_DIR) && ./node_modules/.bin/prettier --write .

fmt-check:
	@unformatted="$$( $(GOFMT) -l $(GO_FILES) )"; \
	if [ -n "$$unformatted" ]; then \
		echo "以下 Go 文件需要执行 gofmt:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	cd $(CONSOLE_DIR) && ./node_modules/.bin/prettier --check .

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

console-check:
	cd $(CONSOLE_DIR) && ./node_modules/.bin/eslint . --max-warnings 0
	cd $(CONSOLE_DIR) && ./node_modules/.bin/tsc -p tsconfig.web.json --noEmit
	cd $(CONSOLE_DIR) && ./node_modules/.bin/tsc -p tsconfig.server.json --noEmit
	cd $(CONSOLE_DIR) && ./node_modules/.bin/vitest run
	cd $(CONSOLE_DIR) && ./node_modules/.bin/vite build
	cd $(CONSOLE_DIR) && ./node_modules/.bin/tsc -p tsconfig.server.build.json

console-e2e:
	cd $(CONSOLE_DIR) && ./node_modules/.bin/playwright test

helm-check:
	helm lint --strict deploy/helm/linkd -f deploy/helm/linkd/examples/external-services.yaml -f deploy/helm/linkd/examples/clusters.yaml -f deploy/helm/linkd/examples/console-ingress.yaml -f deploy/helm/linkd/examples/servicemonitor.yaml
	helm lint --strict deploy/helm/linkd -f deploy/helm/linkd/examples/existing-secrets.yaml
	node --test deploy/helm/linkd/tests/chart.test.mjs

release-check:
	python3 scripts/test_ci_release.py
	NODE_PATH="$(CURDIR)/$(CONSOLE_DIR)/node_modules" node --test scripts/test-prepare-release-chart.cjs
	NODE_PATH="$(CURDIR)/$(CONSOLE_DIR)/node_modules" node --test scripts/test-image-workflow.cjs

check:
	$(MAKE) fmt-check
	$(MAKE) test
	$(MAKE) vet
	$(MAKE) race
	$(MAKE) lint
	$(MAKE) console-check
	$(MAKE) helm-check
	$(MAKE) release-check

image:
	$(CONTAINER) build \
		--build-arg GO_IMAGE=$(GO_IMAGE) \
		--build-arg GOPROXY=$(GOPROXY) \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		-t $(IMAGE):$(IMAGE_TAG) \
		.

console-image:
	$(CONTAINER) build $(CONSOLE_BUILD_FLAGS) \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg NODE_IMAGE=$(NODE_IMAGE) \
		--build-arg NODE_BUILD_OPTIONS="$(NODE_BUILD_OPTIONS)" \
		-t $(CONSOLE_IMAGE):$(IMAGE_TAG) \
		$(CONSOLE_DIR)

eventgen-image:
	$(CONTAINER) build \
		--file Dockerfile.eventgen \
		--build-arg GO_IMAGE=$(GO_IMAGE) \
		--build-arg GOPROXY=$(GOPROXY) \
		--build-arg VERSION=$(EVENTGEN_VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		-t $(EVENTGEN_IMAGE):$(EVENTGEN_VERSION) \
		.
