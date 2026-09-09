#!/bin/sh
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

# 对显式指定的测试集群运行真实协议契约；仅清理测试自身创建的独立索引。
set -eu
if [ "$#" -eq 0 ]; then
  echo "Usage: sh scripts/test-elasticsearch-compatibility.sh http://test-es:9200 [...]" >&2
  exit 2
fi
cd "$(dirname "$0")/.."
for endpoint in "$@"; do
  export LINKD_TEST_ELASTICSEARCH_URL="$endpoint"
  go test -count=1 ./internal/store/elasticsearch ./internal/eventsource/storage ./internal/lifecycle/enrich/datasources
  (cd console && ./node_modules/.bin/vitest run src/server/elasticsearch.integration.test.ts)
done
