#!/usr/bin/env bash
# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

# 在未连 VPN / 无法访问 apiv2.tapd.woa.com 时，用本脚本代替 `gtm commit commit`。
# 从当前分支名末尾的 #单号 生成与 gtm（TAPD）一致的 message：type: 标题 #单号
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
在未连 VPN / 无法访问 apiv2.tapd.woa.com 时，用本脚本代替 gtm commit commit。
从当前分支名末尾的 #单号 生成与 gtm（TAPD）一致的 message。
EOF
  echo "用法: $0 [-y] \"提交说明\"" >&2
  echo "  -y  不询问，直接 git commit -a" >&2
  exit 1
}

assume_yes=false
while getopts "yh" opt; do
  case "$opt" in
    y) assume_yes=true ;;
    h) usage ;;
    *) usage ;;
  esac
done
shift $((OPTIND - 1))

branch=$(git rev-parse --abbrev-ref HEAD)
if [[ "$branch" != *"#"* ]]; then
  echo "错误: 分支名需包含 TAPD 单号，例如 .../#1010158081132689253" >&2
  exit 1
fi

id="${branch##*#}"
if ! [[ "$id" =~ ^[0-9]+$ ]]; then
  echo "错误: 分支名 # 后应为数字单号，当前为: $id" >&2
  exit 1
fi

type=$(echo "$branch" | cut -d/ -f1)
case "$type" in
  feat|fix|docs|test|style|chore|refactor|perf) ;;
  *) type=feat ;;
esac

if [[ $# -lt 1 ]]; then
  usage
fi

title="$*"
msg="${type}: ${title} #${id}"

echo "Commit message:"
echo "  $msg"

if ! $assume_yes; then
  read -r -p "执行 git commit -a -m （如上）? [y/N] " ans || true
  if [[ ! "${ans:-}" =~ ^[yY]$ ]]; then
    echo "已取消。"
    exit 0
  fi
fi

git commit -a -m "$msg"
