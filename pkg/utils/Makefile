# REQUIRE COMPONENTS
# https://github.com/mvdan/gofumpt => $go install mvdan.cc/gofumpt@latest
# https://github.com/incu6us/goimports-reviser => $go install github.com/incu6us/goimports-reviser/v3@v3.1.1
# https://github.com/golangci/golangci-lint => $go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.50.1

GO ?= go
SHELL := bash
ROOTPATH ?= $(shell pwd)/../..
VERSION := $(shell cat VERSION)

.PHONY: help
help:
	@echo "Make Targets: "
	@echo " mod: Download and tidy dependencies"
	@echo " lint: Lint Go code"
	@echo " test: Run unit tests"

.PHONY: lint
lint:
	diff -u <(echo -n) <(gofumpt -w .)
	diff -u <(echo -n) <(goimports-reviser -project-name "github.com/TencentBlueKing/bkmonitor-datalink/pkg" ./...)
	@echo "Please check if imports have been changed" && git status
	diff -u <(echo -n) <(golangci-lint run -c ${ROOTPATH}/.golangci.yml)

.PHONY: test
test:
	$(GO) test ./... -buildmode=pie -parallel=8 -cover

.PHONY: mod
mod:
	GO111MODULE=on go mod download
	GO111MODULE=on go mod tidy
