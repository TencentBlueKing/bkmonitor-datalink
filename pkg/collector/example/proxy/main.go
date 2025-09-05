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
	"encoding/json"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"time"
)

const URL = "http://localhost:10205/v2/push/"

type Request struct {
	DataID      int64            `json:"data_id"`
	AccessToken string           `json:"access_token"`
	Data        []map[string]any `json:"data"`
}

func buildInvalidTokenRequest() []byte {
	bs, _ := json.Marshal(Request{
		DataID:      10010,
		AccessToken: "non_exist",
		Data:        []map[string]any{},
	})
	return bs
}

func buildEmptyTokenRequest() []byte {
	bs, _ := json.Marshal(Request{
		Data: []map[string]any{},
	})
	return bs
}

func buildTimeSeriesData() []byte {
	items := make([]map[string]any, 0, 1)
	items = append(items, map[string]any{
		"metrics":   map[string]float64{"cpu_load1": 1.0},
		"dimension": map[string]string{"vm": "node1"},
		"timestamp": time.Now().UnixMilli() + int64(rand.Int31n(200)),
		"target":    "localhost",
	})

	bs, _ := json.Marshal(Request{
		DataID:      1100002,
		AccessToken: "1100002_accesstoken",
		Data:        items,
	})
	return bs
}

func buildEventData() []byte {
	items := make([]map[string]any, 0, 1)
	items = append(items, map[string]any{
		"event_name": "alarm",
		"event":      map[string]string{"content": time.Now().Format(time.RFC3339)},
		"dimension":  map[string]string{"vm": "node1"},
		"timestamp":  time.Now().UnixMilli() + int64(rand.Int31n(200)),
		"target":     "localhost",
	})

	bs, _ := json.Marshal(Request{
		DataID:      1100001,
		AccessToken: "1100001_accesstoken",
		Data:        items,
	})
	return bs
}

func doRequest(b []byte) {
	buf := bytes.NewBuffer(b)
	response, err := http.Post(URL, "", buf)
	if err != nil {
		log.Println("failed to post data, err: ", err)
		return
	}
	defer response.Body.Close()

	b, _ = io.ReadAll(response.Body)
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
			doRequest(buildTimeSeriesData())
			doRequest(buildEventData())
			doRequest(buildInvalidTokenRequest())
			doRequest(buildEmptyTokenRequest())
		}
	}
}
