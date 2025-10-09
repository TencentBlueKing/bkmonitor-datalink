# REQUIRE TOOLS
# https://github.com/mvdan/gofumpt => $go install mvdan.cc/gofumpt@latest
# https://github.com/incu6us/goimports-reviser => $go install github.com/incu6us/goimports-reviser/v3@v3.1.1
# https://github.com/golangci/golangci-lint => $go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.50.1
# https://sigs.k8s.io/controller-tools => $ go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.18.0

GO ?= go
SHELL = bash
PKG = github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator

# 可继承自顶层 Makefile
RELEASE_PATH ?= $(PWD)/dist
VERSION := $(shell cat VERSION)

# 构建二进制
BIN = $(RELEASE_PATH)/bkmonitor-operator

.PHONY: help
help:
	@echo "Make Targets: "
	@echo " mod: Download and tidy dependencies"
	@echo " lint: Lint Go code"
	@echo " test: Run unit tests"
	@echo " build: Build Go package"
	@echo " image: Build and push docker image"
	@echo " codegen: Generate code for CRD"

.PHONY: mod
mod:
	$(GO) mod download
	$(GO) mod tidy

.PHONY: lint
lint:
	gofmt -r 'interface{} -> any' -w .
	diff -u <(echo -n) <(gofumpt -w .)
	diff -u <(echo -n) <(goimports-reviser -project-name "github.com/TencentBlueKing/bkmonitor-datalink/pkg" ./...)

.PHONY: test
test:
	$(GO) test ./... -buildmode=pie -parallel=4 -cover -ldflags "-X google.golang.org/protobuf/reflect/protoregistry.conflictPolicy=ignore"

.PHONY: build
build:
	cd $(RELEASE_PATH) && ls -lh && rm -rf *
	$(GO) build -ldflags " \
	-s -w \
	-X $(PKG)/cmd.Version=$(VERSION) \
	-X $(PKG)/cmd.BuildTime=$(shell date -u '+%Y-%m-%d_%I:%M:%S%p') \
	-X $(PKG)/cmd.GitHash=$(shell git rev-parse HEAD) \
	-X google.golang.org/protobuf/reflect/protoregistry.conflictPolicy=ignore" \
	-o $(BIN) .

.PHONY: image
image: build
	chmod +x $(BIN)
	cp $(BIN) dockerfile/operator/bkmonitor-operator
	cp $(BIN) dockerfile/reloader/bkmonitorbeat-reloader
	cd dockerfile/operator && docker build -t ${MIRRORS}/bkmonitor-operator:$(VERSION) . && docker push ${MIRRORS}/bkmonitor-operator:$(VERSION) && rm bkmonitor-operator
	cd dockerfile/reloader && docker build -t ${MIRRORS}/bkmonitorbeat-reloader:$(VERSION) . && docker push ${MIRRORS}/bkmonitorbeat-reloader:$(VERSION) && rm bkmonitorbeat-reloader

.PHONY: codegen
codegen:
	chmod +x ./hack/update-codegen.sh && bash ./hack/update-codegen.sh
	controller-gen rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=crd/bases
	gofmt -r 'interface{} -> any' -w .
