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
	"strings"
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

func TestServiceInstanceSystemRelationHostIP(t *testing.T) {
	for _, tc := range []struct {
		name       string
		resourceIP any
		hostIP     any
		attrIP     any
		kind       core.SpanKind
		wantIP     string
	}{
		{name: "server attribute only", kind: core.KindServer, attrIP: "192.0.2.1", wantIP: "192.0.2.1"},
		{name: "client attribute alone is not local host", kind: core.KindClient, attrIP: "192.0.2.1"},
		{name: "resource wins", kind: core.KindServer, resourceIP: "192.0.2.2", attrIP: "192.0.2.1", wantIP: "192.0.2.2"},
		{name: "client resource IP remains valid", kind: core.KindClient, resourceIP: "192.0.2.2", wantIP: "192.0.2.2"},
		{name: "empty resource falls back", kind: core.KindServer, resourceIP: "", hostIP: "192.0.2.3", attrIP: "192.0.2.1", wantIP: "192.0.2.3"},
		{name: "empty resource fields fall back to server attribute", kind: core.KindServer, resourceIP: "", hostIP: "", attrIP: "192.0.2.1", wantIP: "192.0.2.1"},
		{name: "missing IP produces no system relation", kind: core.KindServer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resource := map[string]any{"service.name": "service", "bk.instance.id": "instance"}
			if tc.resourceIP != nil {
				resource["net.host.ip"] = tc.resourceIP
			}
			if tc.hostIP != nil {
				resource["host.ip"] = tc.hostIP
			}
			attributes := map[string]any{}
			if tc.attrIP != nil {
				attributes["net.host.ip"] = tc.attrIP
			}
			graph := NewDiGraph()
			graph.AddNode(Node{StandardSpan: ToStandardSpan(Span{
				TraceId: "trace", SpanId: "span", SpanName: "operation",
				Resource: resource, Attributes: attributes, Kind: int(tc.kind),
			})})
			receiver := make(chan storage.SaveRequest, 1)
			processor := MetricProcessor{baseInfo: core.BaseInfo{BkBizId: "2", AppName: "app"}}
			processor.findSpanMetric(receiver, graph)
			request := <-receiver
			labels := request.Data.(storage.PrometheusStorageData).Value.([]string)
			var systemRelation string
			for _, label := range labels {
				if strings.HasPrefix(label, "__name__="+storage.ApmServiceSystemRelation+",") {
					systemRelation = label
					break
				}
			}
			if tc.wantIP == "" {
				assert.Empty(t, systemRelation)
			} else {
				assert.Contains(t, systemRelation, "bk_target_ip="+tc.wantIP)
			}
		})
	}
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
