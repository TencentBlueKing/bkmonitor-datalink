SHELL := /bin/sh

GO ?= go
GOFMT ?= gofmt
GOLANGCI_LINT ?= golangci-lint
IMAGE ?= linkd
IMAGE_TAG ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
VERSION ?= $(IMAGE_TAG)
GIT_COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
GO_IMAGE ?= golang:1.26.10
GOPROXY ?= direct
CONTAINER ?= $(shell command -v docker >/dev/null 2>&1 && echo docker || echo container)
EVENTGEN_IMAGE ?= linkd-eventgen
EVENTGEN_VERSION ?= 0.2.0
CLI_INSTALL_DIR ?= $(HOME)/.local/bin

GO_FILES := $(shell find . -type f -name '*.go' -not -path './vendor/*' -not -path './scripts/node_modules/*' | LC_ALL=C sort)

.PHONY: fmt fmt-check test race vet lint helm-check release-check check image eventgen-image cli cli-install

fmt:
	$(GOFMT) -w $(GO_FILES)

cli:
	$(GO) build -trimpath -ldflags "-X main.version=$(VERSION) -X main.gitCommit=$(GIT_COMMIT)" -o bin/linkd-cli ./cmd/linkd-cli

cli-install: cli
	install -d "$(CLI_INSTALL_DIR)"
	install -m 0755 bin/linkd-cli "$(CLI_INSTALL_DIR)/linkd-cli"

fmt-check:
	@unformatted="$$( $(GOFMT) -l $(GO_FILES) )"; \
	if [ -n "$$unformatted" ]; then \
		echo "以下 Go 文件需要执行 gofmt:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

helm-check:
	helm lint --strict deploy/helm/linkd -f deploy/helm/linkd/examples/external-services.yaml -f deploy/helm/linkd/examples/clusters.yaml -f deploy/helm/linkd/examples/servicemonitor.yaml
	helm lint --strict deploy/helm/linkd -f deploy/helm/linkd/examples/existing-secrets.yaml
	node --test deploy/helm/linkd/tests/chart.test.mjs

release-check:
	python3 scripts/test_ci_release.py
	node --test scripts/test-prepare-release-chart.cjs
	node --test scripts/test-image-workflow.cjs

check:
	$(MAKE) fmt-check
	$(MAKE) test
	$(MAKE) vet
	$(MAKE) race
	$(MAKE) lint
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

eventgen-image:
	$(CONTAINER) build \
		--file Dockerfile.eventgen \
		--build-arg GO_IMAGE=$(GO_IMAGE) \
		--build-arg GOPROXY=$(GOPROXY) \
		--build-arg VERSION=$(EVENTGEN_VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		-t $(EVENTGEN_IMAGE):$(EVENTGEN_VERSION) \
		.
