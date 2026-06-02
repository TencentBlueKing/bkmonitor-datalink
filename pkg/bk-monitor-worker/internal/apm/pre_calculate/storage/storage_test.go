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
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/remote"
)

func TestProxyInstance_WriteBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dataId := "12345"
	appKey := core.AppKey{BkBizId: "2", AppName: "testApp"}
	core.InitMetadataCenter(&core.MetadataCenter{Mapping: &sync.Map{}})
	core.GetMetadataCenter().AddDataIdAndInfo(
		dataId,
		dataId,
		core.DataIdInfo{
			BaseInfo: core.BaseInfo{BkBizId: appKey.BkBizId, AppName: appKey.AppName},
		},
	)

	proxy, err := NewProxyInstance(
		dataId, ctx,
		WorkerCount(1),
		SaveHoldMaxCount(1),
		SaveHoldDuration(time.Second),
		PrometheusWriterConfig(
			remote.PrometheusWriterUrl(config.PromRemoteWriteUrl),
			remote.PrometheusWriterHeaders(config.PromRemoteWriteHeaders),
		),
		MetricsConfig(
			MetricRelationMemDuration(10*time.Minute),
			MetricFlowMemDuration(time.Minute),
			MetricFlowBuckets(config.MetricsDurationBuckets),
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
			Data: PrometheusStorageData{
				AppKey: appKey,
				Kind:   PromRelationMetric,
				Value: []string{
					"__name__=storage_test,role=child,status=failed",
					"__name__=storage_test,role=admin,status=failed",
				},
			},
		}
	}()

	time.Sleep(time.Second * 1)
	cancel()

	<-ctx.Done()
}
