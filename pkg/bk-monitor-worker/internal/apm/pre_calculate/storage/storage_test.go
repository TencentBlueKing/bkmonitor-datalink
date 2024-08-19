// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"context"
	"log"
	"math/rand"
	"testing"
	"time"

	"github.com/prometheus/prometheus/prompb"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/remote"
)

func TestProxyInstance_WriteBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mocker.InitTestDBConfig("../../../../dist/bmw.yaml")

	proxy, err := NewProxyInstance(
		"", ctx,
		WorkerCount(1),
		SaveHoldMaxCount(1),
		SaveHoldDuration(time.Second),
		PrometheusWriterConfig(
			remote.PrometheusWriterEnabled(true),
			remote.PrometheusWriterUrl(config.PromRemoteWriteUrl),
			remote.PrometheusWriterHeaders(config.PromRemoteWriteHeaders),
		),
	)
	if err != nil {
		log.Fatal(err)
	}

	errChan := make(chan error)
	proxy.Run(errChan)

	dataChan := proxy.SaveRequest()
	go func() {
		dataChan <- SaveRequest{
			Target: Prometheus,
			Data: remote.PrometheusStorageData{
				Value: []prompb.TimeSeries{
					{
						Labels: []prompb.Label{
							{
								Name:  "__name__",
								Value: "storage_test",
							},
							{
								Name:  "role",
								Value: "child",
							},
							{
								Name:  "status",
								Value: "failed",
							},
						},
						Samples: []prompb.Sample{
							{
								Timestamp: time.Now().UnixMilli(),
								Value:     rand.Float64() * float64(rand.Intn(100)),
							},
						},
					},
					{
						Labels: []prompb.Label{
							{
								Name:  "__name__",
								Value: "storage_test",
							},
							{
								Name:  "role",
								Value: "admin",
							},
							{
								Name:  "status",
								Value: "failed",
							},
						},
						Samples: []prompb.Sample{
							{
								Timestamp: time.Now().UnixMilli(),
								Value:     rand.Float64() * float64(rand.Intn(100)),
							},
						},
					},
				},
			},
		}
	}()

	time.Sleep(time.Second * 1)
	cancel()

	<-ctx.Done()
}
