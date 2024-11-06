#!/usr/bin/env bash

# Copyright 2017 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

CODE_GENERATOR_FILE="./vendor/k8s.io/code-generator/generate-groups.sh"

if [ ! -f "${CODE_GENERATOR_FILE}" ]; then
  go mod vendor
fi

chmod +x ${CODE_GENERATOR_FILE}

set -o errexit
set -o nounset
set -o pipefail

ROOT_DIR="../../../../../"
MODULE="github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator"
TYPES="deepcopy,client,informer,lister"
OUTPUT_PKG=${MODULE}"/client"
APIS_PKG=${MODULE}"/apis"
GROUP_VERSIONS="crd:v1beta1 bk.tencent.com:v1alpha1"
HEADER_FILE="./hack/boilerplate.go.txt"

# generate the code with:
# --output-base    because this script should also be able to run inside the vendor dir of
#                  k8s.io/kubernetes. The output-base is needed for the generators to output into the vendor dir
#                  instead of the $GOPATH directly. For normal projects this can be dropped.
bash ${CODE_GENERATOR_FILE} \
  "${TYPES}" \
  "${OUTPUT_PKG}" \
  "${APIS_PKG}" \
  "${GROUP_VERSIONS}" \
  --go-header-file ${HEADER_FILE} \
  --output-base ${ROOT_DIR}