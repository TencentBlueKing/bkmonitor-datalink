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
	URL = "http://localhost:4318/v1/logpush"

	template = `{"source_id":"123456","source_time":"%s","alarm_context":{"key1":"value1","key2":"value2"},"target_type":"HOST","message":"Now is %s","my-index":%d}
`
)

func doRequest(n int) {
	buf := &bytes.Buffer{}
	for i := 0; i < n; i++ {
		t := time.Now().Format(time.RFC3339Nano)
		buf.WriteString(fmt.Sprintf(template, t, t, i))
	}

	request, _ := http.NewRequest(http.MethodPost, URL, buf)
	request.Header.Set("Content-Type", "application/jsonl")
	request.Header.Set("X-BK-TOKEN", "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw==")

	request.Header.Set("X-BK-METADATA", "user=mando,env=fortest")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		log.Printf("post data failed, err: %v\n", err)
		return
	}
	defer response.Body.Close()

	b, err := io.ReadAll(response.Body)
	if err != nil {
		log.Printf("read response failed, err: %v\n", err)
		return
	}
	log.Println(string(b))
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
			doRequest(10)
		}
	}
}
