#!/usr/bin/env bash
set -euo pipefail
task_root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cli_repo=${BLACKBOX_CLI_REPO:-"$task_root/../.."}
server_repo=${BLACKBOX_SERVER_REPO:-}
if [ -z "$server_repo" ]; then
  if [ -f "$cli_repo/../alarmd/go.mod" ]; then
    server_repo="$cli_repo/../.."
  else
    server_repo="$cli_repo/../bkmonitor-datalink"
  fi
fi
task_work=$(mktemp -d)
trap 'rm -rf "$task_work"' EXIT
mkdir -p "$task_root/bin"
export BLACKBOX_CLI_BIN="$task_root/bin/alarmd-cli"
export BLACKBOX_RESULT_DIR=${BLACKBOX_RESULT_DIR:-"$task_root/results"}
export BLACKBOX_CLI_REPO="$cli_repo"
export BLACKBOX_SERVER_REPO="$server_repo"
(cd "$cli_repo" && go build -buildvcs=false -o "$BLACKBOX_CLI_BIN" .)
cp "$task_root/go.mod" "$task_work/blackbox.mod"
cp "$task_root/go.sum" "$task_work/blackbox.sum"
cd "$task_root"
go mod edit -modfile="$task_work/blackbox.mod" "-replace=github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd=$server_repo/pkg/alarmd"
go mod tidy -modfile="$task_work/blackbox.mod"
go test -modfile="$task_work/blackbox.mod" -count=1 -v .
