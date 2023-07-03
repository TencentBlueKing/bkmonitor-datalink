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
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	promcollectors "github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/push"
)

var uptime = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "uptime",
	}, []string{"env", "endpoint"},
)

var duration = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "request_duration",
		Buckets: []float64{0.1, 0.5, 1, 5, 10, 60},
	},
)

func init() {
	go func() {
		for range time.Tick(time.Second * 1) {
			uptime.WithLabelValues("PROD", "localhost").Add(1)
			c, err := uptime.GetMetricWithLabelValues("PROD", "localhost")
			if err != nil {
				panic(err)
			}
			c.(prometheus.ExemplarAdder).AddWithExemplar(1024,
				map[string]string{
					"traceID": fmt.Sprintf("my_trace_id_%d", time.Now().UnixMilli()),
					"spanID":  fmt.Sprintf("my_span_id_%d", time.Now().UnixMilli()),
				},
			)
			duration.Observe(0.3)
		}
	}()
}

type bkClient struct{}

func (c *bkClient) Do(r *http.Request) (*http.Response, error) {
	// X-BK-TOKEN 设置有两种方式，TOKEN 即在 saas 侧申请的 token
	// 如：Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw==
	//
	// 1) headers 新增 X-BK-TOKEN 字段
	r.Header.Set("X-BK-TOKEN", "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw==")

	// 2) query 参数新增 X-BK-TOKEN 字段
	// r.URL.RawQuery = r.URL.RawQuery + "X-BK-TOKEN=${TOKEN}"
	return http.DefaultClient.Do(r)
}

func main() {
	register := prometheus.NewRegistry()
	register.MustRegister(uptime, duration, promcollectors.NewGoCollector())

	name := "demo"
	pusher := push.New("localhost:4318", name).Gatherer(register).Grouping("instance", "my.host.ip").Grouping("biz", "mando")
	pusher.Client(&bkClient{})

	// pusher.Format(expfmt.FmtText)
	ticker := time.Tick(10 * time.Second)
	for {
		<-ticker
		if err := pusher.Push(); err != nil {
			log.Println("failed to push records to the server, error:", err)
			continue
		}
		log.Println("push records to the server successfully")
	}
}
