#!/bin/bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.


sizes=(100 300 500 700 1000 3000 5000 7000 10000 30000)

for size in ${sizes[@]}
do
    echo size: ${size}
    for i in HashMap BSTMap Map BBolt Badger SkipList
    do
        echo type: ${i}
        env TRANSFER_BENCH_SIZE=${size} go test -run ^$ -failfast -tags "${i} `tr [A-Z] [a-z] <<< ${i}`" -bench . transfer/storage -timeout 180m -benchmem "$@" || exit $?
    done
done
