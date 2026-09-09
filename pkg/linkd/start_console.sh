#!/usr/bin/env bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

# 使用 PM2 构建并启动 Linkd Console。

set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
console_dir="${root_dir}/console"
prometheus_config="${root_dir}/configs/prometheus.console.local.yaml"
prometheus_container="linkd-prometheus"
prometheus_port="${LINKD_CONSOLE_PROMETHEUS_PORT:-19090}"
process_name="linkd-console"

for command_name in node pnpm pm2 docker; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "缺少命令: ${command_name}" >&2
    exit 1
  fi
done

if [[ -n "${LINKD_CONFIG:-}" ]]; then
  if [[ "${LINKD_CONFIG}" = /* ]]; then
    linkd_config="${LINKD_CONFIG}"
  else
    linkd_config="${root_dir}/${LINKD_CONFIG}"
  fi
elif [[ -f "${root_dir}/configs/linkd.pm2.local.yaml" ]]; then
  linkd_config="${root_dir}/configs/linkd.pm2.local.yaml"
else
  linkd_config="${root_dir}/configs/linkd.pm2.yaml"
fi

if [[ ! -f "${linkd_config}" ]]; then
  echo "Linkd 配置文件不存在: ${linkd_config}" >&2
  exit 1
fi

cat >"${prometheus_config}" <<'EOF'
global:
  scrape_interval: 5s
  evaluation_interval: 5s

scrape_configs:
  - job_name: linkd
    static_configs:
      - targets: ["host.docker.internal:9464"]
EOF

if docker container inspect "${prometheus_container}" >/dev/null 2>&1; then
  docker rm -f "${prometheus_container}" >/dev/null
fi
docker run -d \
  --name "${prometheus_container}" \
  --restart unless-stopped \
  -p "127.0.0.1:${prometheus_port}:9090" \
  -v "${prometheus_config}:/etc/prometheus/prometheus.yml:ro" \
  prom/prometheus:v3.5.0 \
  --config.file=/etc/prometheus/prometheus.yml \
  --storage.tsdb.retention.time=7d >/dev/null

cd "${console_dir}"
pnpm install --frozen-lockfile
pnpm build

export NODE_ENV="production"
export LINKD_CONFIG="${linkd_config}"
export LINKD_CONSOLE_HOST="${LINKD_CONSOLE_HOST:-127.0.0.1}"
export LINKD_CONSOLE_PORT="${LINKD_CONSOLE_PORT:-4399}"
export LINKD_CONSOLE_PROMETHEUS_URL="${LINKD_CONSOLE_PROMETHEUS_URL:-http://127.0.0.1:${prometheus_port}}"

if pm2 describe "${process_name}" >/dev/null 2>&1; then
  pm2 restart "${process_name}" --update-env
else
  pm2 start "${console_dir}/dist-server/server/index.js" \
    --name "${process_name}" \
    --cwd "${console_dir}" \
    --interpreter node \
    --time
fi

pm2 status "${process_name}"
echo "Prometheus: http://127.0.0.1:${prometheus_port}"
echo "Linkd Console: http://${LINKD_CONSOLE_HOST}:${LINKD_CONSOLE_PORT}"
