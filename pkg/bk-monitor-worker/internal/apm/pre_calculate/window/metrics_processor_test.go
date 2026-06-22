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
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/storage"
)

func TestMetricsHandleResult(t *testing.T) {
	dataId := "12345"
	appKey := core.AppKey{BkBizId: "2", AppName: "testApp"}
	p := initialProcessor(t, dataId, true)

	t.Run("single-trace", func(t *testing.T) {
		actual := runMetricCase(p, "single.json")
		expected := []storage.SaveRequest{
			{
				Target: storage.Prometheus,
				Data: storage.PrometheusStorageData{
					AppKey: appKey,
					Kind:   storage.PromRelationMetric,
					Value:  sortedLabels(fileExceptToTypeInstance("single-expect-metrics.json", "list").([]string)),
				},
			},
		}
		assert.Equal(t, expected, actual)
	})

	t.Run("complex-trace", func(t *testing.T) {
		actual := runMetricCase(p, "complex.json")
		expected := []storage.SaveRequest{
			{
				Target: storage.Prometheus,
				Data: storage.PrometheusStorageData{
					AppKey: appKey,
					Kind:   storage.PromRelationMetric,
					Value:  sortedLabels(fileExceptToTypeInstance("complex-expect-metrics-relation.json", "list").([]string)),
				},
			},
			{
				Target: storage.Prometheus,
				Data: storage.PrometheusStorageData{
					AppKey: appKey,
					Kind:   storage.PromFlowMetric,
					Value:  sortedLabels(fileExceptToTypeInstance("complex-expect-metrics-flow.json", "list").([]string)),
				},
			},
		}
		assert.Equal(t, expected, actual)
	})
}

func TestDynamicRelationFlowMetric(t *testing.T) {
	dataId := "12345"
	p := initialProcessor(t, dataId, true)
	p.metricProcessor.dynamicRelationFlowReportEnabled = true

	actual := runMetricCase(p, "complex.json")
	expect := "__name__=system_to_system_flow,from_bk_target_ip=192.168.0.1,to_bk_target_ip=192.168.0.2"
	for _, request := range actual {
		data, ok := request.Data.(storage.PrometheusStorageData)
		if !ok || data.Kind != storage.PromRelationMetric {
			continue
		}
		assert.Contains(t, prometheusLabels(data.Value), expect)
		return
	}
	t.Fatalf("dynamic relation metric request not found")
}

func runMetricCase(p Processor, traceFileName string) []storage.SaveRequest {
	event := fileTracesToEvent(traceFileName)
	resultChan := make(chan storage.SaveRequest, 1000)
	p.PreProcess(resultChan, event)
	return normalizePrometheusRequests(drainSaveRequests(resultChan, len(resultChan)))
}

func drainSaveRequests(resultChan chan storage.SaveRequest, count int) []storage.SaveRequest {
	requests := make([]storage.SaveRequest, 0, count)
	for i := 0; i < count; i++ {
		requests = append(requests, <-resultChan)
	}
	return requests
}

func normalizePrometheusRequests(requests []storage.SaveRequest) []storage.SaveRequest {
	grouped := make(map[core.AppKey]map[int][]string)
	for _, request := range requests {
		if request.Target != storage.Prometheus {
			continue
		}
		data := request.Data.(storage.PrometheusStorageData)
		if grouped[data.AppKey] == nil {
			grouped[data.AppKey] = make(map[int][]string)
		}
		grouped[data.AppKey][data.Kind] = append(grouped[data.AppKey][data.Kind], prometheusLabels(data.Value)...)
	}

	normalized := make([]storage.SaveRequest, 0, len(grouped))
	for appKey, byKind := range grouped {
		kinds := make([]int, 0, len(byKind))
		for kind := range byKind {
			kinds = append(kinds, kind)
		}
		sort.Ints(kinds)
		for _, kind := range kinds {
			normalized = append(normalized, storage.SaveRequest{
				Target: storage.Prometheus,
				Data: storage.PrometheusStorageData{
					AppKey: appKey,
					Kind:   kind,
					Value:  sortedLabels(byKind[kind]),
				},
			})
		}
	}

	sort.Slice(normalized, func(i, j int) bool {
		left := normalized[i].Data.(storage.PrometheusStorageData)
		right := normalized[j].Data.(storage.PrometheusStorageData)
		if left.AppKey != right.AppKey {
			if left.AppKey.BkBizId != right.AppKey.BkBizId {
				return left.AppKey.BkBizId < right.AppKey.BkBizId
			}
			return left.AppKey.AppName < right.AppKey.AppName
		}
		return left.Kind < right.Kind
	})
	return normalized
}

func prometheusLabels(value any) []string {
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...)
	case map[string]*storage.FlowMetricRecordStats:
		labels := make([]string, 0, len(v))
		for label := range v {
			labels = append(labels, label)
		}
		return labels
	default:
		return nil
	}
}

func sortedLabels(labels []string) []string {
	res := append([]string(nil), labels...)
	sort.Strings(res)
	return res
}
