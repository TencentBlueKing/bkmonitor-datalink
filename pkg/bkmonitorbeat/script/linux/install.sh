#!/bin/bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.


# check system type
source ./env.sh
kernel=`uname -a | grep "x86_64" | wc -l`
procName=${cmd}_amd64

if [[ ${kernel} -eq 1 ]]; then
    procName=${cmd}_amd64
else
    procName=${cmd}_386
fi

if [[ "`md5sum ${procName} | cut -d ' ' -f 1`" = "`cat ${procName}.md5`" ]]; then
    ln -sf ${procName} ${cmd}
else
    echo ${procName} is invalid
    md5sum ${procName}
    exit 1
fi
