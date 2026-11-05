GO ?= go
SHELL := bash

# 可继承自顶层 Makefile
RELEASE_PATH ?= $(PWD)/dist
VERSION := $(shell cat VERSION)
JSON_LIB ?= jsonsonic
BUILDTAGS ?= $(JSON_LIB)

# 构建二进制
BIN = $(RELEASE_PATH)/bk-collector

# 节点管理 V3 插件包输出目录，默认与 V2 插件包放在同一目录
RELEASE_PATH_V3 ?= $(RELEASE_PATH)
# 节点管理不校验插件包文件名（插件名与版本取自包内 project.yaml），加上 nodeman-v3 以区分 V2 包
PACKAGE_V3 = bk-collector-nodeman-v3-$(VERSION).tgz
# V3 插件包中间目录，不放在 RELEASE_PATH 下，避免混入 V2 产物
BUILD_PATH_V3 ?= $(CURDIR)/build/nodeman-v3

.PHONY: help
help:
	@echo "Make Targets: "
	@echo " mod: Download and tidy dependencies"
	@echo " lint: Lint Go code"
	@echo " encode: Encode template file in base64 format"
	@echo " test: Run unit tests"
	@echo " build: Build release package"
	@echo " build-v3: Build NodeMan V3 plugin package"
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

# 同时产出 V2、V3 插件包；V3 须在 V2 打包（tar -C . *）之后生成，否则会被打进 V2 插件包
.PHONY: build
build:
	cd $(RELEASE_PATH) && ls -lh && rm -rf *
	JSON_LIB=$(JSON_LIB) ./control.sh package linux amd64 x86_64 $(VERSION) $(RELEASE_PATH)
	cd $(RELEASE_PATH) && ls -lh && tar -zcvf bk-collector-$(VERSION).tgz --remove-files -C . * && cp bk-collector-$(VERSION).tgz bk-collector.tgz
	$(MAKE) build-v3
	JSON_LIB=$(JSON_LIB) ./control.sh sidecar $(VERSION) $(RELEASE_PATH) && ls -lh $(RELEASE_PATH)

# 节点管理 V3 插件包：$(RELEASE_PATH_V3)/$(PACKAGE_V3)，包内顶层目录为 bk-collector/
.PHONY: build-v3
build-v3:
	rm -rf $(BUILD_PATH_V3) && mkdir -p $(BUILD_PATH_V3) $(RELEASE_PATH_V3)
	JSON_LIB=$(JSON_LIB) ./control.sh package_v3 linux amd64 x86_64 $(VERSION) $(BUILD_PATH_V3)
	tar -zcvf $(RELEASE_PATH_V3)/$(PACKAGE_V3) -C $(BUILD_PATH_V3) bk-collector
	rm -rf $(BUILD_PATH_V3) && ls -lh $(RELEASE_PATH_V3)/$(PACKAGE_V3)

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
