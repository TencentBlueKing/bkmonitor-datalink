#!/bin/bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.


if [[ "$1" = "" ]]
then
    cmd="usage"
else
    cmd=./$1.sh
fi

shift 1

if [[ -e "${cmd}" ]]
then
    "${cmd}" "$@" || exit $?
else
    cmds=`ls *.sh`
    cmds=${cmds//`basename $0`/usage}
    cmds=${cmds//.sh/}
    echo "usage: "${cmds}
    exit 1
fi
