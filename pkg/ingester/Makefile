ROOTPATH ?= $(shell pwd)/../..
RELEASE_PATH ?= ${PWD}/bin
BUILD_NO ?= 0
NAME = ingester
VERSION ?= $(shell cat VERSION)
COMMIT_ID ?= $(shell git rev-parse HEAD)

APPPATH = "github.com/TencentBlueKing/bkmonitor-datalink/pkg/${NAME}"
PACKAGES = $(shell go list -f '{{.ImportPath}}' ./...)

BUILDTAGS ?= go_metrics bbolt BBolt elasticsearch_v5 elasticsearch_v6 elasticsearch_v7 redis_v2
LDFLAGS=" \
		-X ${APPPATH}/define.Version=${VERSION} \
		-X ${APPPATH}/define.AppName=${NAME} \
		-X ${APPPATH}/define.Mode=release \
		-X ${APPPATH}/define.BuildHash=${COMMIT_ID} \
		"

.PHONY: build
build: tidy
	mkdir -p $(RELEASE_PATH)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags ${LDFLAGS} -o ${RELEASE_PATH}/${APPNAME} ${APPPATH}

.PHONY: debug
debug: tidy
	mkdir -p $(RELEASE_PATH)
	go build -ldflags ${LDFLAGS} -o ${RELEASE_PATH}/${APPNAME} ${APPPATH}

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: test
test:
	go test -timeout 1m -tags "$(BUILDTAGS)" -parallel 8  ./...

.PHONY: generate
generate:
	go generate -x ${PACKAGES}

.PHONY: lint
lint:
	golangci-lint run -c ${ROOTPATH}/.golangci.yml
