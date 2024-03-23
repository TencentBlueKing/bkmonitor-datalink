GO ?= go
SHELL := bash

# 可继承自顶层 Makefile
RELEASE_PATH ?= $(PWD)/dist
VERSION := $(shell cat VERSION)

# 构建二进制
BIN = $(RELEASE_PATH)/sliwebhook

.PHONY: help
help:
	@echo "Make Targets: "
	@echo " mod: Download and tidy dependencies"
	@echo " lint: Lint Go code"
	@echo " test: Run unit tests"
	@echo " dev: Run and test program locally"
	@echo " build: Build binary distribution"
	@echo " install: Install dev tools"

.PHONY: lint
lint: mod
	diff -u <(echo -n) <(gofumpt -w .)
	diff -u <(echo -n) <(goimports-reviser -project-name "github.com/TencentBlueKing/bkmonitor-datalink/pkg" ./...)

.PHONY: test
test:
	$(GO) test ./...

.PHONY: mod
mod:
	$(GO) mod download
	$(GO) mod tidy

.PHONY: build
build:
	GOOS=linux GOARCH=amd64 \
	$(GO) build -ldflags " \
	-s -w \
	-X main.version=$(VERSION) \
	-X main.buildTime=$(shell date -u '+%Y-%m-%d_%I:%M:%S%p') \
	-X main.gitHash=$(shell git rev-parse HEAD)" \
	-o $(BIN) .

.PHONY: dev
dev:
	$(GO) run . ./sliwebhook.yaml

.PHONY: install
install:
	$(GO) install mvdan.cc/gofumpt@latest
	$(GO) install github.com/incu6us/goimports-reviser/v3@v3.1.1
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.50.1
