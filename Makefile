PWD = $(shell pwd)
MODULES ?= bkmonitorbeat collector operator transfer unify-query influxdb-proxy ingester offline-data-archive bk-monitor-worker sliwebhook
RELEASE_PATH ?= $(PWD)/dist
BUILD_NO ?= 1
COMMIT_ID = $(shell git rev-parse HEAD)
MODULE = ''
RELEASE ?= false

MODULE_VERSION = $(subst v,ee-V, $(shell cat $(PWD)/pkg/$(MODULE)/VERSION||echo ''))
BRANCH ?= $(shell git symbolic-ref --short HEAD)

ifeq ($(RELEASE), true)
	PACKAGE_VERSION = $(MODULE_VERSION)
else
	PACKAGE_VERSION = $(MODULE_VERSION)-$(BRANCH)
endif

VERSION = $(subst v,,$(subst x,$(BUILD_NO),$(shell cat $(PWD)/pkg/$(MODULE)/VERSION || echo '')))
TAG = pkg/$(MODULE)/v$(VERSION)
PIP_PATH ?= $(shell which pip)

.PHONY: all
all: bkmonitorbeat collector operator transfer unify-query influxdb-proxy ingester offline-data-archive bk-monitor-worker sliwebhook

.PHONY: .check_module_vars
.check_module_vars:
	@test $${MODULE?Please set environment variable MODULE}

.PHONY: build
build: .check_module_vars
	mkdir -p $(RELEASE_PATH)/$(MODULE)
	@echo make module: $(MODULE) BUILD_NO: $(BUILD_NO) COMMIT_ID: $(COMMIT_ID)
	cd $(PWD)/pkg/$(MODULE) && make RELEASE_PATH=$(RELEASE_PATH)/$(MODULE) VERSION=$(VERSION) BUILD_NO=$(BUILD_NO) COMMIT_ID=$(COMMIT_ID) JSON_LIB=$(JSON_LIB) build

.PHONY: bkmonitorbeat
bkmonitorbeat:
	$(MAKE) MODULE=bkmonitorbeat build

.PHONY: collector
collector:
	$(MAKE) MODULE=collector build

.PHONY: operator
operator:
	$(MAKE) MODULE=operator build

.PHONY: sliwebhook
sliwebhook:
	$(MAKE) MODULE=sliwebhook build

.PHONY: transfer
transfer:
	$(MAKE) MODULE=transfer build

.PHONY: unify-query
unify-query:
	$(MAKE) MODULE=unify-query build

.PHONY: influxdb-proxy
influxdb-proxy:
	$(MAKE) MODULE=influxdb-proxy build

.PHONY: ingester
ingester:
	$(MAKE) MODULE=ingester build

.PHONY: offline-data-archive
offline-data-archive:
	$(MAKE) MODULE=offline-data-archive build

.PHONY: bk-monitor-worker
bk-monitor-worker:
	$(MAKE) MODULE=bk-monitor-worker build

.PHONY: version
version: .check_module_vars
	@echo $(VERSION)

.PHONY: package_version
package_version:
	@echo $(PACKAGE_VERSION)

.PHONY: tag
tag: .check_module_vars
	cd $(PWD)/pkg/$(MODULE) || exit 1
	@echo tag: $(TAG)
	git tag $(TAG)
	git push --tags

.PHONY: lint
lint:
	cd $(PWD)/pkg/$(MODULE) && make lint

.PHONY: test
test:
	cd $(PWD)/pkg/$(MODULE) && make test

.PHONY: fmt
fmt:
	cd $(PWD)/pkg/$(MODULE) && go fmt ./...

.PHONY: debug
debug: .check_module_vars
	mkdir -p $(RELEASE_PATH)/$(MODULE)
	@echo make module: $(MODULE) BUILD_NO: $(BUILD_NO) COMMIT_ID: $(COMMIT_ID)
	cd $(PWD)/pkg/$(MODULE) && make RELEASE_PATH=$(RELEASE_PATH)/$(MODULE) VERSION=$(VERSION) BUILD_NO=$(BUILD_NO) COMMIT_ID=$(COMMIT_ID) JSON_LIB=$(JSON_LIB) debug

.PHONY: pre-commit
pre-commit:
	pre-commit run -a

.check_pip_vars:
	@test $(PIP_PATH)||(echo 'pip command or PIP_PATH not found'&&exit 1)

.PHONY: pre-commit-install
pre-commit-install: .check_pip_vars
	$(PIP_PATH) install -r scripts/requirements.txt
	go install github.com/google/addlicense@latest
	go install github.com/incu6us/goimports-reviser/v3@latest
	pre-commit clean
	pre-commit install

.PHONY: addlicense
addlicense:
	find ./ -type f \( -iname \*.go -o -iname \*.py -iname \*.sh \)|xargs addlicense -v -f scripts/license.txt -ignore vendor/*

.PHONY: imports
imports: addlicense
	goimports-reviser -project-name "github.com/TencentBlueKing/bkmonitor-datalink/pkg" ./...