# REQUIRE TOOLS
# https://github.com/mvdan/gofumpt => $go install mvdan.cc/gofumpt@latest
# https://github.com/incu6us/goimports-reviser => $go install github.com/incu6us/goimports-reviser/v3@v3.1.1
# https://github.com/golangci/golangci-lint => $go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.50.1

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

.PHONY: lint
lint: mod
	diff -u <(echo -n) <(gofumpt -w .)
	diff -u <(echo -n) <(goimports-reviser -project-name "github.com/TencentBlueKing/bkmonitor-datalink/pkg" ./...)
	diff -u <(echo -n) <(golangci-lint run -c ../../.golangci.yml)

.PHONY: test
test: mod
	$(GO) test ./... -buildmode=pie -parallel=8 -cover

.PHONY: mod
mod:
	$(GO) mod download
	$(GO) mod tidy

.PHONY: build
build: mod
	cd $(RELEASE_PATH) && ls -lh && rm -rf *
	GOOS=linux GOARCH=amd64 \
	go build -ldflags " \
	-s -w \
	-X $(PKG)/cmd.Version=$(VERSION) \
	-X $(PKG)/cmd.BuildTime=$(shell date -u '+%Y-%m-%d_%I:%M:%S%p') \
	-X $(PKG)/cmd.GitHash=$(shell git rev-parse HEAD)" \
	-o $(BIN) .

.PHONY: image
image: build
	chmod +x $(BIN)
	cp $(BIN) dockerfile/operator/bkmonitor-operator
	cp $(BIN) dockerfile/reloader/bkmonitorbeat-reloader
	cd dockerfile/operator && docker build -t ${MIRRORS}/bkmonitor-operator:$(VERSION) . && docker push ${MIRRORS}/bkmonitor-operator:$(VERSION) && rm bkmonitor-operator
	cd dockerfile/reloader && docker build -t ${MIRRORS}/bkmonitorbeat-reloader:$(VERSION) . && docker push ${MIRRORS}/bkmonitorbeat-reloader:$(VERSION) && rm bkmonitorbeat-reloader
