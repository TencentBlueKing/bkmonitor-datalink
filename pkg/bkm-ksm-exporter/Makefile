GO ?= go
SHELL := bash

# Inheritable from the top-level Makefile.
RELEASE_PATH ?= $(PWD)/dist
VERSION := $(shell cat VERSION)
BIN = $(RELEASE_PATH)/bkm-ksm-exporter

.PHONY: help
help:
	@echo "Make Targets:"
	@echo " mod:   Download and tidy dependencies"
	@echo " lint:  Lint Go code"
	@echo " test:  Run unit tests"
	@echo " bin:   Build the linux/amd64 binary"
	@echo " build: Alias of bin (entry point for the top-level Makefile)"
	@echo " dev:   Run locally against \$$KUBECONFIG"

.PHONY: mod
mod:
	$(GO) mod download
	$(GO) mod tidy

.PHONY: lint
lint: mod
	diff -u <(echo -n) <(gofumpt -w .)
	diff -u <(echo -n) <(goimports-reviser -project-name "github.com/TencentBlueKing/bkmonitor-datalink/pkg" ./...)

.PHONY: test
test:
	$(GO) test ./... -cover

.PHONY: bin
bin:
	mkdir -p $(RELEASE_PATH)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
	$(GO) build -ldflags " \
	-s -w \
	-X main.version=$(VERSION) \
	-X main.buildTime=$(shell date -u '+%Y-%m-%d_%I:%M:%S%p') \
	-X main.gitHash=$(shell git rev-parse HEAD)" \
	-o $(BIN) ./cmd/bkm-ksm-exporter

.PHONY: build
build: bin

.PHONY: dev
dev:
	$(GO) run ./cmd/bkm-ksm-exporter -kubeconfig $${KUBECONFIG:-$$HOME/.kube/config}
