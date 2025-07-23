#!/usr/bin/env bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

error_count=0
for file in "$@"
do
  addlicense -check -f scripts/license.txt -ignore vendor/* "$file"
  ret=$?
  if [ $ret -ne 0 ]; then
    echo "missing license: $file ret: $ret"
    error_count=$((error_count+1))
  fi
done

if [ $error_count -gt 0 ]; then
  echo "total: $error_count, run 'make addlicense' to fix"
  exit 1
fi
