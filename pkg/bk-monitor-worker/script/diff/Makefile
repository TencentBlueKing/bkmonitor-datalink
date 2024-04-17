.PHONY: build test tidy

SERVICENAME=diff
WORKSPACE=$(shell pwd)

BINPATH=${WORKSPACE}/bin

# build service
build: tidy
	go build -o ${BINPATH}/${SERVICENAME} ${WORKSPACE}/diff.go

build-linux: tidy
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ${BINPATH}/${SERVICENAME} ${WORKSPACE}/diff.go

# test
test:
	go test -v ./...  -cover -count=1 -vet=off

tidy:
	go mod tidy
