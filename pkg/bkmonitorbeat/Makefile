RELEASE_PATH ?= bin
BUILD_NO ?= 0
NAME = bkmonitorbeat
VERSION = ''
COMMIT_ID = ''

.PHONY: lint
lint:
	gofumpt -w .
	goimports-reviser -project-name "github.com/TencentBlueKing/bkmonitor-datalink/pkg" ./...

.PHONY: build
build:
	env VERSION=$(VERSION) RELEASE_PATH=$(RELEASE_PATH) ./script/gse_package.sh

.PHONY: test
test:
	go test ./...
