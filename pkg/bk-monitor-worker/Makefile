.PHONY: build test tidy

SERVICENAME=bk-monitor-worker
WORKSPACE=$(shell pwd)
JSON_LIB ?= jsonsonic
BUILDTAGS ?= $(JSON_LIB)

ifdef VERSION
    VERSION=${VERSION}
else
    VERSION=$(shell git describe --always)
endif

ifdef RELEASE_PATH
	RELEASEPATH=${RELEASE_PATH}
else
	RELEASEPATH=${WORKSPACE}/bin
endif

ifdef COMMIT_ID
	COMMIT_ID=${COMMIT_ID}
else
	COMMIT_ID=$(shell git rev-parse HEAD)
endif

LDFLAGS="-X github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/versions.Version=${VERSION} \
	-X github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/versions.GitCommit=${COMMIT_ID} \
	-X github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/versions.BuildTime=${shell date +%Y-%m-%dT%I:%M:%S} \
	-X github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config.ServiceName=${SERVICENAME}"


# build service
build: tidy
	go build -tags "$(BUILDTAGS)" -ldflags ${LDFLAGS} -o ${RELEASEPATH}/${SERVICENAME} ./bmw.go

build-linux: tidy
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags "$(BUILDTAGS)" -ldflags ${LDFLAGS} -o ${RELEASEPATH}/${SERVICENAME} ./bmw.go

# test
test:
	go test -tags "$(BUILDTAGS)" -v ./...  -cover -count=1 -vet=off

tidy:
	go mod tidy

.PHONY: gen
gen:
	#find . -type f -name 'qs_*_gen.go' -exec rm -f {} \;
	go generate ./...

.PHONY: fmt
fmt:
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
