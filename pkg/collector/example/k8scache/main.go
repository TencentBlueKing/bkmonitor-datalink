// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"log"
	"net/http"
)

const content = `
{
    "resourceVersion": 10,
    "pods": [
        {
            "action": "CreateOrUpdate",
            "cluster": "BCS-K8S-00000",
            "name": "bkm-statefulset-worker-0",
            "namespace": "bkmonitor-operator",
            "ip": "127.0.0.1"
        },
        {
            "action": "CreateOrUpdate",
            "cluster": "BCS-K8S-00000",
            "name": "bkm-statefulset-worker-1",
            "namespace": "bkmonitor-operator",
            "ip": "::1"
        }
    ]
}
`

func main() {
	http.HandleFunc("/pods", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(content))
	})

	addr := ":7090"
	log.Printf("start listen on %s\n", addr)
	http.ListenAndServe(addr, nil)
}
