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
	"bytes"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"
)

const (
	URL = "http://localhost:4318/v1/logbeat"

	content = `
{
    "gseindex": 3,
    "items": [
        {
            "data": "2024-06-11 20:41:32.711 WARN    host/watcher.go:301     something wrong here",
            "iterationindex": 0
        }
    ],
    "time": 1718175766,
    "utctime": "2024-06-12 07:02:46",
    "dataid": 1001,
    "datetime": "2024-06-12 15:02:46",
    "ext": {
        "io_kubernetes_workload_type": "DaemonSet",
        "container_name": "bkunifylogbeat-bklog",
        "io_kubernetes_pod": "bk-log-collector-tsrwh",
        "io_kubernetes_pod_namespace": "kube-system",
        "io_kubernetes_pod_uid": "59bcb51c-8aa1-4306-8e80-a7d09f31657d",
        "io_kubernetes_workload_name": "bk-log-collector"
    },
    "filename": "/your/path/app.log"
}`
)

func doRequest() {
	request, _ := http.NewRequest(http.MethodPost, URL, bytes.NewBufferString(content))
	request.Header.Set("X-BK-DATA-ID", "1001")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		log.Printf("post data failed, err: %v\n", err)
		return
	}
	defer response.Body.Close()

	_, err = io.ReadAll(response.Body)
	if err != nil {
		log.Printf("read response failed, err: %v\n", err)
		return
	}
	log.Println("status code:", response.StatusCode)
}

func main() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c:
			return

		case <-ticker.C:
			doRequest()
		}
	}
}
