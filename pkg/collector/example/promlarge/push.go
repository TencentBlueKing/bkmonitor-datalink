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
	_ "embed"
	"flag"
	"net/http"
	"sync"
	"time"
)

const URL = "http://localhost:4318/metrics/job/promtest"

var (
	confRequests    int64
	confInterval    string
	confConcurrency int64
	confToken       string
)

//go:embed metrics.txt
var metricsTxt string

func init() {
	flag.Int64Var(&confRequests, "requests", 1, "number of requests")
	flag.StringVar(&confInterval, "interval", "1s", "interval between each requests")
	flag.Int64Var(&confConcurrency, "concurrency", 1, "concurrency of requests")
	flag.StringVar(&confToken, "token", "", "apm token")
	flag.Parse()
}

func ConfRequests() int {
	if confRequests <= 0 {
		return 1
	}
	return int(confRequests)
}

func ConfInterval() time.Duration {
	d, err := time.ParseDuration(confInterval)
	if err != nil {
		return time.Second
	}
	return d
}

func ConfConcurrency() int {
	if confConcurrency <= 0 {
		return 1
	}
	return int(confConcurrency)
}

func Metrics() string {
	return metricsTxt
}

func Token() string {
	if confToken == "" {
		return "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw=="
	}
	return confToken
}

func main() {
	ch := make(chan struct{}, ConfConcurrency())
	wg := sync.WaitGroup{}

	for i := 0; i < ConfConcurrency(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range ch {
				_, _ = http.Post(URL+"?X-BK-TOKEN="+Token(), "", bytes.NewBufferString(Metrics()))
			}
		}()
	}

	for i := 0; i < ConfRequests(); i++ {
		time.Sleep(ConfInterval())
		ch <- struct{}{}
	}
	close(ch)
	wg.Wait()
}
