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

MODULE_NAME=bkmonitorbeat
VERSION=${VERSION}
SOURCE_PATH=.
DIST_PATH=${RELEASE_PATH}
BUILD_PATH=./build
WITH_FREEBSD=0

setEnv() {
  echo "setEnv $1=$2"
  export "$1"="$2"
}

make_package () {
    # 编译单个os_arch的版本目录
    local operating_system=$1
    local cpu_arch=$2
    local plugin_name=$3
    local plugin_version=$4
    local dir_name=${BUILD_PATH}/plugins_${operating_system}_${cpu_arch}/${plugin_name}
    local yaml=${dir_name}/project.yaml
    local bin_dir etc_dir suffix
    templates_dir=templates

    [ $operating_system == "windows" ] && suffix=".exe"

     # 如果support-files有对应的配置则打包，否则直接返回
    if [ -e support-files/${templates_dir}/${operating_system}/${cpu_arch} ] ; then
        [ -e ${dir_name} ] && rm -rf ${dir_name}
        mkdir -p ${dir_name}/{etc,bin}
        bin_dir=${dir_name}/bin
        etc_dir=${dir_name}/etc

        case $cpu_arch in
            x86_64)
                go_arch=amd64
                ;;
            x86)
                go_arch=386
                ;;
            aarch64)
                go_arch=arm64
                ;;
            *)
                go_arch=$cpu_arch
                ;;
        esac

        if [ ${operating_system} = "linux" ] || [ ${operating_system} = "windows" ] ; then
            #VERSION=$(cat VERSION).$(git describe --dirty="-dev" --always --match "NOT A TAG")
             #GO111MODULE=off
            CGO_ENABLED=0 GO111MODULE=on GOOS=${operating_system} GOARCH=${go_arch} go build -tags "basetask basescheduler bkmonitorbeat" -ldflags=" -X main.BeatName=${plugin_name} -X main.Version=${plugin_version}" -o ${bin_dir}/${plugin_name}${suffix} main.go||exit 1
        elif [ ${operating_system} = "freebsd" ]; then
            cp ${plugin_name}_${operating_system} ${bin_dir}/${plugin_name}${suffix}
        else
            echo "${operating_system}不支持"
            exit 1
        fi

        cp -R support-files/${templates_dir}/${operating_system}/${cpu_arch}/project.yaml ${yaml}
        sed -i -r "s/(\s)?version: [0-9a-zA-Z].*/\1version: ${plugin_version}/g" ${yaml}
        sed -i "s/plugin_version: \"\*\"/plugin_version: ${plugin_version}/g" ${yaml}

        cp -R support-files/${templates_dir}/${operating_system}/${cpu_arch}/etc ${dir_name}/

        # 去掉子配置文件模板中的注释，保留主配置文件的注释内容
        sed -i '/^ *#/d' ${etc_dir}/${plugin_name}_*.conf*
    else
      echo "${operating_system}无对应support-files"
    fi
}


package() {
  # 3. 准备打包目录
  [ -e ${BUILD_PATH} ] && rm -rf ${BUILD_PATH}
  mkdir -p ${BUILD_PATH}


  # 4. 编译  按系统版本
  for OS in linux windows; do
      for CPU_ARCH in x86 x86_64; do
          make_package $OS $CPU_ARCH ${MODULE_NAME} ${VER}
      done
  done

  # arm64的版本
  make_package linux aarch64 ${MODULE_NAME} ${VER}
  if [ $WITH_FREEBSD -ge 1 ]; then
    # freebsd的版本
    make_package freebsd x86_64 ${MODULE_NAME} ${VER}
  fi


  # 5. 打包
  PACKAGE_NAME_PREFIX=${MODULE_NAME}-${VER}
  if [ $WITH_FREEBSD -ge 1 ]; then
    PACKAGE_NAME_PREFIX=${PACKAGE_NAME_PREFIX}-with-freebsd
  fi

  PACKAGE_NAME=${PACKAGE_NAME_PREFIX}.tgz
  echo "PACKAGE_NAME: ${PACKAGE_NAME}"
  rm -f ${WORKSPACE}/${MODULE_NAME}*.tgz ${WORKSPACE}/${MODULE_NAME}*.commit

  cd ${BUILD_PATH} && tar czf ${PACKAGE_NAME} * && cd -
  cp ${BUILD_PATH}/${PACKAGE_NAME} ${DIST_PATH}/${PACKAGE_NAME}
  cp ${BUILD_PATH}/${PACKAGE_NAME} ${DIST_PATH}/${MODULE_NAME}.tgz && rm ${BUILD_PATH}/${PACKAGE_NAME}

  echo "${LAST_COMMIT_ID}" > ${DIST_PATH}/${LAST_COMMIT_ID_FILE_NAME}
  rm -rf ${BUILD_PATH}
}

function build() {
  VER=${1#v}
  if [ "$VER" = "" ]; then
    echo "invalid version: $VER"
    exit 1
  fi
  echo "VER: $VER"

  # 1.2 获取到最后一次提交ID
  cd ${SOURCE_PATH}
  export LAST_COMMIT_ID=`git rev-parse HEAD`
  export LAST_COMMIT_ID_FILE_NAME=${MODULE_NAME}-${VER}.commit

  package
}

# go 版本和地址
echo `which go`
echo `go version`
[ -e ${DIST_PATH} ] && rm -rf ${DIST_PATH}
mkdir -p ${DIST_PATH}
build $VERSION
