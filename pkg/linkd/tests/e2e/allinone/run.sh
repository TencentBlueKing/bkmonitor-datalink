#!/usr/bin/env bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../../.." && pwd)"

export LINKD_E2E=1
export LINKD_E2E_ELASTICSEARCH_URL="${LINKD_E2E_ELASTICSEARCH_URL:-http://127.0.0.1:9200}"
export LINKD_E2E_REDIS_ADDRESS="${LINKD_E2E_REDIS_ADDRESS:-127.0.0.1:16379}"
export LINKD_E2E_REDIS_PASSWORD="${LINKD_E2E_REDIS_PASSWORD:-test123456}"
export LINKD_E2E_REDIS_DATABASE="${LINKD_E2E_REDIS_DATABASE:-0}"
export LINKD_E2E_KAFKA_BROKER="${LINKD_E2E_KAFKA_BROKER:-127.0.0.1:9092}"
export LINKD_E2E_MYSQL_ADDRESS="${LINKD_E2E_MYSQL_ADDRESS:-127.0.0.1:13306}"
export LINKD_E2E_MYSQL_USERNAME="${LINKD_E2E_MYSQL_USERNAME:-root}"
export LINKD_E2E_MYSQL_PASSWORD="${LINKD_E2E_MYSQL_PASSWORD:-test123456}"
export GOCACHE="${GOCACHE:-/tmp/linkd-e2e-go-cache}"

cd "${repo_root}"
exec go test -count=1 -run '^TestAllInOne(Elasticsearch|MySQL)E2E$' -v ./tests/e2e/allinone
