GO ?= go
SHELL := bash

# 可继承自顶层 Makefile
RELEASE_PATH ?= $(PWD)/dist
VERSION := $(shell cat VERSION)
JSON_LIB ?= jsonsonic
BUILDTAGS ?= $(JSON_LIB)

# 构建二进制
BIN = $(RELEASE_PATH)/bk-collector

.PHONY: help
help:
	@echo "Make Targets: "
	@echo " mod: Download and tidy dependencies"
	@echo " lint: Lint Go code"
	@echo " encode: Encode template file in base64 format"
	@echo " test: Run unit tests"
	@echo " build: Build release package"
	@echo " dev: Run and test program locally"
	@echo " bin: Build binary distribution"
	@echo " install: Install dev tools"

.PHONY: lint
lint:
	gofmt -r 'interface{} -> any' -w .
	diff -u <(echo -n) <(gofumpt -w .)
	diff -u <(echo -n) <(goimports-reviser -project-name "github.com/TencentBlueKing/bkmonitor-datalink/pkg" ./...)

.PHONY: test
test:
	./control.sh test

.PHONY: encode
encode:
	./control.sh encode

.PHONY: build
build:
	cd $(RELEASE_PATH) && ls -lh && rm -rf *
	JSON_LIB=$(JSON_LIB) ./control.sh package linux amd64 x86_64 $(VERSION) $(RELEASE_PATH)
	cd $(RELEASE_PATH) && ls -lh && tar -zcvf bk-collector-$(VERSION).tgz --remove-files -C . * && cp bk-collector-$(VERSION).tgz bk-collector.tgz
	JSON_LIB=$(JSON_LIB) ./control.sh sidecar $(VERSION) $(RELEASE_PATH) && ls -lh $(RELEASE_PATH)

.PHONY: mod
mod:
	$(GO) mod download
	$(GO) mod tidy

.PHONY: bin
bin:
	GOOS=linux GOARCH=amd64 \
	$(GO) build -tags "$(BUILDTAGS)" -ldflags " \
	-s -w \
	-X main.version=$(VERSION) \
	-X main.buildTime=$(shell date -u '+%Y-%m-%d_%I:%M:%S%p') \
	-X main.gitHash=$(shell git rev-parse HEAD)" \
	-o $(BIN) ./cmd/collector

.PHONY: dev
dev:
	$(GO) run ./cmd/collector -c ./example/example.yml

.PHONY: install
install:
	$(GO) install mvdan.cc/gofumpt@v0.5.0
	$(GO) install github.com/incu6us/goimports-reviser/v3@v3.1.1
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.50.1
