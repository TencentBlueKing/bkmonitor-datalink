RELEASE_PATH ?= bin
BUILD_NO ?= 0
NAME = bkmonitorbeat
VERSION = ''
COMMIT_ID = ''

.PHONY: build
build:
	env VERSION=$(VERSION) RELEASE_PATH=$(RELEASE_PATH) ./script/gse_package.sh

.PHONY: test
test:
	go test ./...

