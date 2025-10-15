#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
#CODEGEN_PKG=${CODEGEN_PKG:-$(go list -m -f '{{.Dir}}' k8s.io/code-generator)}
#
#bash "${CODEGEN_PKG}"/generate-groups.sh \
#  "client,lister,informer" \
#  pkg/generated \
#  api \
#  bk.tencent.com:v1alpha1 \
#  --go-header-file "${SCRIPT_ROOT}"/hack/boilerplate.go.txt \
#  --output-base "${SCRIPT_ROOT}" \
#  -v 10

CODEGEN_PKG=$(go list -m -f '{{.Dir}}' k8s.io/code-generator)

bash "${CODEGEN_PKG}"/generate-groups.sh \
  "client,lister,informer" \
  github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/pkg/generated \
  github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/api \
  bk.tencent.com:v1alpha1 \
  --output-base "${SCRIPT_ROOT}" \
  --go-header-file ./boilerplate.go.txt \
  -v 10