// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BK-Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/remote"
)

type recordingRelationReporter struct {
	requests []prompb.TimeSeries
	err      error
	closed   int
}

func (r *recordingRelationReporter) Write(_ context.Context, series ...prompb.TimeSeries) error {
	r.requests = append(r.requests, series...)
	return r.err
}
func (r *recordingRelationReporter) Close(context.Context) error { r.closed++; return nil }

func TestMetricDimensionsHandlerDualWritesRelationAndFlow(t *testing.T) {
	for _, tc := range []struct {
		name        string
		enabled     bool
		apmStatus   int
		relationErr error
	}{
		{name: "disabled", apmStatus: 204},
		{name: "both targets", enabled: true, apmStatus: 204},
		{name: "relation failure preserves APM", enabled: true, apmStatus: 204, relationErr: errors.New("target unavailable")},
		{name: "APM failure preserves relation", enabled: true, apmStatus: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var received []prompb.TimeSeries
			var receivedMu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "app-token", r.Header.Get("X-BK-TOKEN"))
				compressed, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				body, err := snappy.Decode(nil, compressed)
				require.NoError(t, err)
				var req prompb.WriteRequest
				require.NoError(t, proto.Unmarshal(body, &req))
				receivedMu.Lock()
				received = append(received, req.Timeseries...)
				receivedMu.Unlock()
				w.WriteHeader(tc.apmStatus)
			}))
			defer server.Close()
			h := NewMetricDimensionHandler(context.Background(), "123", core.BaseInfo{BkBizId: "2", AppName: "app-a", Token: "app-token"}, remote.PrometheusWriterOptions{Url: server.URL}, MetricConfigOptions{relationMetricMemDuration: time.Hour, flowMetricMemDuration: time.Hour, flowMetricBuckets: []float64{100, 1000}})
			reporter := &recordingRelationReporter{err: tc.relationErr}
			if tc.enabled {
				h.relationReporter = reporter
			}
			h.Add(PrometheusStorageData{Kind: PromRelationMetric, Value: []string{"__name__=apm_service_with_apm_service_instance_relation,apm_application_name=app-a,apm_service_name=service-a,apm_service_instance_name=instance-a"}})
			h.Add(PrometheusStorageData{Kind: PromFlowMetric, Value: map[string]*FlowMetricRecordStats{
				"__name__=apm_service_to_apm_service_flow,apm_application_name=app-a": {DurationValues: []float64{50, 200}},
			}})
			// Relation batches are emitted after their aggregation interval.
			h.relationMetricDimensions.mu.Lock()
			for key := range h.relationMetricDimensions.data {
				h.relationMetricDimensions.data[key] = time.Now().Add(-2 * time.Hour)
			}
			h.relationMetricDimensions.mu.Unlock()
			// Close must stop both collectors, flush both kinds, and close the reporter once.
			h.Close()
			h.Close()
			receivedMu.Lock()
			defer receivedMu.Unlock()
			names := map[string]bool{}
			for _, s := range received {
				for _, l := range s.Labels {
					if l.Name == "__name__" {
						names[l.Value] = true
					}
				}
			}
			for _, name := range []string{ApmServiceInstanceRelation, ApmServiceFlow + "_bucket", ApmServiceFlow + "_sum", ApmServiceFlow + "_count", ApmServiceFlow + "_min", ApmServiceFlow + "_max"} {
				require.True(t, names[name], name)
			}
			if tc.enabled {
				require.Equal(t, received, reporter.requests)
				require.Equal(t, 1, reporter.closed)
			} else {
				require.Empty(t, reporter.requests)
				require.Nil(t, h.relationReporter)
			}
		})
	}
}

func TestMetricDimensionsHandlerConfiguredReporterAndConcurrentClose(t *testing.T) {
	var cfg MetricConfigOptions
	MetricRelationDataID(123)(&cfg)
	MetricRelationMemDuration(time.Hour)(&cfg)
	MetricFlowMemDuration(time.Hour)(&cfg)
	h := NewMetricDimensionHandler(context.Background(), "456", core.BaseInfo{AppName: "app"}, remote.PrometheusWriterOptions{Url: "http://127.0.0.1"}, cfg)
	require.NotNil(t, h.relationReporter)
	// Empty collectors must not trigger token lookup or network writes.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); h.Close() }()
	}
	wg.Wait()
}
