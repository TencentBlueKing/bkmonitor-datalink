ROOTPATH ?= $(shell pwd)/../..
RELEASE_PATH ?= ${PWD}/bin
BUILD_NO ?= 0
NAME = unify-query
VERSION ?=$(shell cat VERSION)
COMMIT_ID ?= $(shell git rev-parse HEAD)
JSON_LIB ?= jsonsonic
BUILDTAGS ?= $(JSON_LIB)

LDFLAGS="-X github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config.Version=${VERSION} \
		-X github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config.CommitHash=${COMMIT_ID}"

.PHONY: build
build: tidy
	mkdir -p $(RELEASE_PATH)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags "$(BUILDTAGS)" -ldflags $(LDFLAGS) -o $(RELEASE_PATH)/$(NAME) ./main.go

.PHONY: debug
debug: tidy
	mkdir -p $(RELEASE_PATH)
	go build -tags "$(BUILDTAGS)" -ldflags $(LDFLAGS) -o $(RELEASE_PATH)/$(NAME) ./main.go

.PHONY: upx
upx: build
	cd $(RELEASE_PATH)
	upx -9 $(RELEASE_PATH)/$(NAME)

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: test
test:
	go test -tags "$(BUILDTAGS)" -cover -timeout 1m -parallel 8  ./...

.PHONY: swag
swag:
	swag init --parseDependency true --parseInternal true

.PHONY: fmt
fmt:
	go vet
	gofmt -r 'interface{} -> any' -w .
	gofumpt -w .

.PHONY: lint
lint: fmt
	golangci-lint run -c ${ROOTPATH}/.golangci.yml

.PHONY: addlicense
addlicense: fmt
	find ./ -type f \( -iname \*.go -o -iname \*.py -iname \*.sh \) | xargs addlicense -v -f ../../scripts/license.txt -ignore vendor/*

.PHONY: imports
imports: addlicense
	goimports-reviser -rm-unused -set-alias -format -project-name "github.com/TencentBlueKing/bkmonitor-datalink/pkg" ./...

.PHONY: race
race:
	go test -race -timeout 1m -parallel 8  ./...