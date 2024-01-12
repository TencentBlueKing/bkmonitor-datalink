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
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"
)

const (
	URL = "http://localhost:4318/fta/v1/event/"

	template = `{
    "ip": "127.0.0.1", 
    "source_id": "123456",    
    "source_time": "%s",   
    "alarm_type": "api_default",
    "alarm_content": "FAILURE for production/HTTP on machine 127.0.0.1", 
    "alarm_context": {"key1":"value1","key2":"value2"}, 
    "description": "avg(usage) > 90%%, 当前值 99%%",
    "target_type": "HOST",
    "category": "os",
    "severity": 1,
    "bk_biz_id": 2
}`
)

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
			data := fmt.Sprintf(template, time.Now().Format("2006-01-02 15:04:05+07:00"))
			request, _ := http.NewRequest(http.MethodPost, URL, bytes.NewBufferString(data))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-BK-TOKEN", "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw==")

			response, err := http.DefaultClient.Do(request)
			if err != nil {
				log.Printf("post data failed, err: %v\n", err)
				continue
			}
			b, _ := io.ReadAll(response.Body)
			log.Println(string(b))
		}
	}
}
