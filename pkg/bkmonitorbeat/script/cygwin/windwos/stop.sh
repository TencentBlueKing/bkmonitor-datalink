#!/bin/bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

source ./env.sh

kiil_cmd()
{
    pid=`ps -elf | grep -w "${cmd}" | grep -v grep | awk '{if ($3 == 1) print $2}'`
    if [[ $pid == "" ]]; then
        # proc not exist
        exit 0
    fi
    echo "kill: $pid"
    kill $pid
}

kiil_cmd
# wait proc stop, max 5s
max=5
while true
do
    kiil_cmd
    # wait until timeout
    sleep 1
    let max-=1
    echo 'wait: '$max
    if (( $max <= 0)); then
        exit 1
    fi
done
