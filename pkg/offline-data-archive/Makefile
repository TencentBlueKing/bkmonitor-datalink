ROOTPATH ?= $(shell pwd)/../..
RELEASE_PATH ?= ${PWD}/bin
BUILD_NO ?= 0
NAME = offline-data-archive
VERSION ?=$(shell cat VERSION)
COMMIT_ID ?= $(shell git rev-parse HEAD)

LDFLAGS="-X github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/config.Version=${VERSION} \
		-X github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/config.CommitHash=${COMMIT_ID}"

.PHONY: build
build: tidy
	mkdir -p $(RELEASE_PATH)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags $(LDFLAGS) -o $(RELEASE_PATH)/$(NAME) ./main.go

.PHONY: start
start: tidy
	go run main.go

.PHONY: debug
debug: tidy
	mkdir -p $(RELEASE_PATH)
	go build -ldflags $(LDFLAGS) -o $(RELEASE_PATH)/$(NAME) ./main.go

.PHONY: upx
upx: build
	cd $(RELEASE_PATH)
	upx -9 $(RELEASE_PATH)/$(NAME)

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: test
test:
	go test -timeout 1m -parallel 8  ./...

.PHONY: swag
swag:
	swag init

.PHONY: fmt
fmt:
	gofmt -w -s .

.PHONY: lint
lint:
	golangci-lint run -c ${ROOTPATH}/.golangci.yml