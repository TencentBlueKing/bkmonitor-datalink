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
	"testing"
	"time"

	"github.com/prometheus/prometheus/prompb"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/remote"
)

type staticMetricCollector struct {
	request prompb.WriteRequest
}

func (c staticMetricCollector) Observe(any) {}

func (c staticMetricCollector) Collect() prompb.WriteRequest {
	return c.request
}

func (c staticMetricCollector) Ttl() time.Duration {
	return time.Minute
}

type recordingRelationReporter struct {
	writes []prompb.TimeSeries
}

func (r *recordingRelationReporter) Write(_ context.Context, series ...prompb.TimeSeries) error {
	r.writes = append(r.writes, series...)
	return nil
}

func (r *recordingRelationReporter) Close(context.Context) error {
	return nil
}

func TestMetricDimensionsHandlerWritesOnlyRelationMetricsToRelationDataID(t *testing.T) {
	relationReporter := &recordingRelationReporter{}
	series := prompb.TimeSeries{Labels: []prompb.Label{{Name: "__name__", Value: "service_with_host"}}}
	handler := &MetricDimensionsHandler{
		dataId:           "12345",
		promClient:       remote.NewPrometheusWriterClient("", "http://127.0.0.1", nil),
		relationReporter: relationReporter,
	}
	collector := staticMetricCollector{request: prompb.WriteRequest{Timeseries: []prompb.TimeSeries{series}}}

	handler.cleanUpAndReport(collector, true)
	assert.Equal(t, []prompb.TimeSeries{series}, relationReporter.writes)

	handler.cleanUpAndReport(collector, false)
	assert.Equal(t, []prompb.TimeSeries{series}, relationReporter.writes)
}
