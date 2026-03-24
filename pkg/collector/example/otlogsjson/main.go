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

func doPost(b []byte) {
	req, _ := http.NewRequest(http.MethodPost, "http://localhost:4318/v1/logs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BK-TOKEN", "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw==")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("failed to post request: %v\n", err)
		return
	}

	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("failed to read response body: %v\n", err)
		return
	}
	log.Printf("response.status_code=%d, body=%s\n", resp.StatusCode, string(buf))
}

func main() {
	f, err := os.Open("logs.json")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	buf, err := io.ReadAll(f)
	if err != nil {
		log.Fatal(err)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c:
			return
		case <-ticker.C:
			doPost(buf)
		}
	}
}
