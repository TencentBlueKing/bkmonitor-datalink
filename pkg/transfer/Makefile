# REQUIRE COMPONENTS
# https://github.com/mvdan/gofumpt => $go install mvdan.cc/gofumpt@latest
# https://github.com/incu6us/goimports-reviser => $go install github.com/incu6us/goimports-reviser/v3@v3.1.1
# https://github.com/golangci/golangci-lint => $go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.50.1

MODULE = transfer
SHELL = bash
GO ?= go
PKG = github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer
BUILDTAGS ?= bbolt elasticsearch_v5 elasticsearch_v6 elasticsearch_v7
JSON_LIB ?= jsonsonic

# 可继承自顶层 Makefile
RELEASE_PATH ?= $(PWD)/dist
VERSION = $(shell cat VERSION)

# 构建二进制
BIN = $(RELEASE_PATH)/$(MODULE)

.PHONY: help
help:
	@echo "Make Targets: "
	@echo " mod: Download and tidy dependencies"
	@echo " lint: Lint Go code"
	@echo " test: Run unit tests"
	@echo " build: Build Go package"

.PHONY: build
build: mod
	cd $(RELEASE_PATH) && ls -lh && rm -rf *
	$(GO) build -tags "$(BUILDTAGS) $(JSON_LIB)" -ldflags=" \
	-s -w \
	-X $(PKG)/define.Version=$(VERSION) \
	-X $(PKG)/define.AppName=$(MODULE) \
	-X $(PKG)/define.Mode=release \
	-X $(PKG)/define.BuildHash=$(shell git rev-parse HEAD)" \
	-o $(BIN) .
	upx -9 $(BIN)

.PHONY: test
test: mod
	$(GO) test -timeout 70s -tags "$(BUILDTAGS) $(JSON_LIB)" -parallel 8  ./...

.PHONY: lint
lint: mod
	diff -u <(echo -n) <(gofumpt -w .)
	diff -u <(echo -n) <(goimports-reviser -project-name "github.com/TencentBlueKing/bkmonitor-datalink/pkg" ./...)
	diff -u <(echo -n) <(golangci-lint run -c ../../.golangci.yml)

.PHONY: mod
mod:
	$(GO) mod download
	$(GO) mod tidy
