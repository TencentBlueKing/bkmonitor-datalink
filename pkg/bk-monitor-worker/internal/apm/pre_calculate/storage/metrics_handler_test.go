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
	"testing"
	"time"

	"github.com/prometheus/prometheus/prompb"
	"github.com/stretchr/testify/require"
)

type mockPrometheusWriter struct {
	writeRequests []prompb.WriteRequest
	closed        bool
}

func (m *mockPrometheusWriter) WriteBatch(_ context.Context, _ string, writeReq prompb.WriteRequest) error {
	m.writeRequests = append(m.writeRequests, writeReq)
	return nil
}

func (m *mockPrometheusWriter) Close(_ context.Context) error {
	m.closed = true
	return nil
}

type mockBuiltinRelationReporter struct {
	spaceUIDs  []string
	timeseries [][]prompb.TimeSeries
	closed     bool
}

func (m *mockBuiltinRelationReporter) Do(_ context.Context, spaceUID string, tsList ...prompb.TimeSeries) error {
	m.spaceUIDs = append(m.spaceUIDs, spaceUID)
	m.timeseries = append(m.timeseries, tsList)
	return nil
}

func (m *mockBuiltinRelationReporter) Close(_ context.Context) error {
	m.closed = true
	return nil
}

type mockMetricCollector struct {
	writeReq prompb.WriteRequest
}

func (m *mockMetricCollector) Observe(_ any) {}

func (m *mockMetricCollector) Collect() prompb.WriteRequest {
	return m.writeReq
}

func (m *mockMetricCollector) Ttl() time.Duration {
	return time.Minute
}

func TestMetricConfigOptionsBuiltinRelationEnabledForBiz(t *testing.T) {
	options := MetricConfigOptions{}
	MetricBuiltinRelationReport(true, []string{"7", "42"}, "result-table-detail")(&options)

	require.True(t, options.builtinRelationEnabledForBiz("7"))
	require.True(t, options.builtinRelationEnabledForBiz("42"))
	require.False(t, options.builtinRelationEnabledForBiz("2"))
	require.Equal(t, "result-table-detail", options.builtinRelationDetailKey)

	MetricBuiltinRelationReport(false, []string{"7"}, "result-table-detail")(&options)
	require.False(t, options.builtinRelationEnabledForBiz("7"))
}

func TestMetricDimensionsHandlerCleanUpAndReportDualWrite(t *testing.T) {
	promWriter := &mockPrometheusWriter{}
	builtinReporter := &mockBuiltinRelationReporter{}
	writeReq := prompb.WriteRequest{Timeseries: []prompb.TimeSeries{
		{
			Labels:  []prompb.Label{{Name: "__name__", Value: "apm_service_instance_with_system_relation"}},
			Samples: []prompb.Sample{{Value: 1, Timestamp: 1}},
		},
	}}
	handler := &MetricDimensionsHandler{
		dataId:                  "12345",
		promClient:              promWriter,
		builtinRelationReporter: builtinReporter,
		builtinRelationSpaceUID: "bkcc__7",
	}

	handler.cleanUpAndReport(&mockMetricCollector{writeReq: writeReq})

	require.Len(t, promWriter.writeRequests, 1)
	require.Equal(t, writeReq, promWriter.writeRequests[0])
	require.Equal(t, []string{"bkcc__7"}, builtinReporter.spaceUIDs)
	require.Equal(t, writeReq.Timeseries, builtinReporter.timeseries[0])
}

func TestMetricDimensionsHandlerCleanUpAndReportSkipsEmptyBatch(t *testing.T) {
	promWriter := &mockPrometheusWriter{}
	builtinReporter := &mockBuiltinRelationReporter{}
	handler := &MetricDimensionsHandler{
		dataId:                  "12345",
		promClient:              promWriter,
		builtinRelationReporter: builtinReporter,
		builtinRelationSpaceUID: "bkcc__7",
	}

	handler.cleanUpAndReport(&mockMetricCollector{})

	require.Empty(t, promWriter.writeRequests)
	require.Empty(t, builtinReporter.spaceUIDs)
}

func TestMetricDimensionsHandlerCloseStopsAndClosesReporters(t *testing.T) {
	promWriter := &mockPrometheusWriter{}
	builtinReporter := &mockBuiltinRelationReporter{}
	ctx, cancel := context.WithCancel(context.Background())
	handler := &MetricDimensionsHandler{
		dataId:                   "12345",
		ctx:                      ctx,
		cancel:                   cancel,
		promClient:               promWriter,
		builtinRelationReporter:  builtinReporter,
		builtinRelationSpaceUID:  "bkcc__7",
		relationMetricDimensions: newRelationMetricCollector(time.Minute),
		flowMetricCollector:      newFlowMetricCollector([]float64{1}, time.Minute),
	}

	handler.Close()

	require.True(t, promWriter.closed)
	require.True(t, builtinReporter.closed)
}
