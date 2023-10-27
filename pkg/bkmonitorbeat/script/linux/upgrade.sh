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

source ./env.sh
package=$1
old_dir="${cmd}.old"

if [[ "${package}" = "" ]]; then
    echo "not package specified"
fi

rm -rf ${old_dir} || true
mkdir -p ${old_dir} || true
mv ./${cmd} ${old_dir} || true
mv ./${cmd}_* ${old_dir} || true
mv ./*.sh ${old_dir} || true
mv VERSION ${old_dir} || true

tar xzf "${package}"
./install.sh
./restart.sh
./check.sh
rm -rf ${package}