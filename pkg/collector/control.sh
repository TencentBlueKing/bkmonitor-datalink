#!/bin/bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

set -e

# shellcheck disable=SC2006
# shellcheck disable=SC2086

MODULE=bk-collector
TEST_COVERAGE_THRESHOLD=76

function unittest() {
  go test ./... -coverprofile coverage.tmp -covermode count
  cat coverage.tmp | grep -v '/gen/' | grep -v '_gen.go' | grep -v '/pb/' > coverage.out
  go tool cover -func coverage.out
  echo "Quality Gate: checking test coverage is above threshold ..."
  echo "Threshold             : $TEST_COVERAGE_THRESHOLD%"
  totalCoverage=$(go tool cover -func=coverage.out | grep total | grep -Eo '[0-9]+\.[0-9]+')
  echo "Current test coverage : $totalCoverage %"
  if (($(echo "$totalCoverage $TEST_COVERAGE_THRESHOLD" | awk '{print ($1 > $2)}'))); then
    echo "OK"
  else
    echo "Current test coverage is below threshold. Please add more unit tests or adjust threshold to a lower value."
    echo "FAIL"
    exit 1
  fi
}

function package() {
  # 变量声明
  local goos=${1:-'linux'}
  local goarch=${2:-'amd64'}
  local arch=${3:-'x86_64'}
  local version=${4:-'v0.0.0'}
  local dist=${5:-'./dist'}

  local dir=${dist}/plugins_${goos}_${arch}/${MODULE}

  # 清空并新建文件夹
  [ -e ${dir} ] && rm -rf ${dir}
  mkdir -p ${dir}/{etc,bin}

  # 构建二进制
  go mod tidy
  GOOS=${goos} GOARCH=${goarch} \
    go build -ldflags " \
  	-s -w \
  	-X main.version=${version} \
  	-X main.buildTime=$(date -u '+%Y-%m-%d_%I:%M:%S%p') \
  	-X main.gitHash=$(git rev-parse HEAD)" \
    -o ${dir}/bin/${MODULE} ./cmd/collector

  # 复制配置
  cp -R ./support-files/templates/${goos}/${arch}/project.yaml ${dir}/project.yaml
  sed -i "/^version:/s/.*/version: ${version}/g" ${dir}/project.yaml
  cp -R ./support-files/templates/${goos}/${arch}/etc ${dir}
}

function sidecar() {
    local version=${1:-'v0.0.0'}
    local dist=${2:-'./dist'}
    # 构建二进制
    go mod tidy
    GOOS=linux GOARCH=amd64 \
      go build -ldflags " \
    	-s -w \
    	-X main.version=${version} \
    	-X main.buildTime=$(date -u '+%Y-%m-%d_%I:%M:%S%p') \
    	-X main.gitHash=$(git rev-parse HEAD)" \
      -o ${dist}/sidecar ./cmd/sidecar
}

function encode() {
  local base_dir="./support-files/templates/linux/x86_64/etc"

  tpls=("bk-collector-application.conf.tpl" "bk-collector-platform.conf.tpl" "bk-collector-report-v2.conf.tpl")
  # shellcheck disable=SC2068
  for tpl in ${tpls[@]}
    do echo 'File:' $tpl && cat ${base_dir}/${tpl} | base64 && echo ''
  done
}

if [ "$1" == "package" ]; then
  package $2 $3 $4 $5 $6
elif [ "$1" == "sidecar" ]; then
  sidecar $2 $3
elif [ "$1" == "test" ]; then
  unittest
elif [ "$1" == "encode" ]; then
  encode
fi
