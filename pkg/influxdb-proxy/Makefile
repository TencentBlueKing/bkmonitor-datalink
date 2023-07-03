ROOTPATH ?= $(shell pwd)/../..
RELEASE_PATH ?= ${PWD}/bin
BUILD_NO ?= 0
NAME = influxdb-proxy
VERSION ?= $(shell cat VERSION)
COMMIT_ID ?= $(shell git rev-parse HEAD)

APPPATH = "github.com/TencentBlueKing/bkmonitor-datalink/pkg/${NAME}"
PACKAGES = $(shell go list -f '{{.ImportPath}}' ./...)

APPNAME = tsdbproxy_v2

LDFLAGS=" \
		-X ${APPPATH}/cmd.Version=${VERSION} \
		-X ${APPPATH}/cmd.AppName=${APPNAME} \
		-X ${APPPATH}/cmd.Mode=release \
		-X ${APPPATH}/cmd.BuildHash=${COMMIT_ID} \
		"

.PHONY: build
build: tidy
	mkdir -p $(RELEASE_PATH)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags ${LDFLAGS} -o ${RELEASE_PATH}/${NAME} ${APPPATH}

.PHONY: debug
debug: tidy
	mkdir -p $(RELEASE_PATH)
	go build -ldflags ${LDFLAGS} -o ${RELEASE_PATH}/${NAME} ${APPPATH}

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: test
test:
	go test -timeout 1m -parallel 8  ./...

.PHONY: generate
generate:
	go generate -x ${PACKAGES}

.PHONY: lint
lint:
	golangci-lint run -c ${ROOTPATH}/.golangci.yml
