// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package window

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/storage"
)

func TestMetricsHandleResult(t *testing.T) {
	dataId := "12345"
	p := initialProcessor(t, dataId, true)

	resultFilter := func(requests []storage.SaveRequest) []storage.SaveRequest {
		var res []storage.SaveRequest
		for _, i := range requests {
			if i.Target == storage.Prometheus {
				res = append(res, i)
			}
		}
		return res
	}
	t.Run("single-trace", func(t *testing.T) {
		if !runCase(
			p,
			"single.json",
			// 期望的关联指标返回
			[]storage.SaveRequest{
				{
					Target: storage.Prometheus,
					Data: storage.PrometheusStorageData{
						Value: fileExceptToTypeInstance("single-expect-metrics.json", "list").([]string),
					},
				},
			},
			resultFilter) {
			t.Fatal("Not equal")
		}
	})

	t.Run("complex-trace", func(t *testing.T) {
		if !runCase(p, "complex.json",
			// 期望的关联指标返回
			[]storage.SaveRequest{
				// Flow 关系关联指标
				{
					Target: storage.Prometheus,
					Data: storage.PrometheusStorageData{
						Value: fileExceptToTypeInstance("complex-expect-metrics-flow.json", "list").([]string),
					},
				},
				// 父子关系关联指标
				{
					Target: storage.Prometheus,
					Data: storage.PrometheusStorageData{
						Value: fileExceptToTypeInstance("complex-expect-metrics-relation.json", "list").([]string),
					},
				},
			}, resultFilter) {
			t.Fatal("Not equal")
		}
	})
}
