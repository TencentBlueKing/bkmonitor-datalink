// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package skywalking

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pmetric"
	agentv3 "skywalking.apache.org/repo/goapi/collect/language/agent/v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
)

func mockJvmMetrics() *agentv3.JVMMetric {
	return &agentv3.JVMMetric{
		Time: 10010,
		Memory: []*agentv3.Memory{
			{
				IsHeap:    true,
				Init:      1,
				Max:       10,
				Used:      20,
				Committed: 30,
			},
			{
				IsHeap:    false,
				Init:      2,
				Max:       30,
				Used:      40,
				Committed: 50,
			},
		},
		MemoryPool: []*agentv3.MemoryPool{
			{
				Type:      0,
				Init:      1,
				Max:       10,
				Used:      20,
				Committed: 30,
			},
			{
				Type:      1,
				Init:      1,
				Max:       10,
				Used:      20,
				Committed: 30,
			},
			{
				Type:      2,
				Init:      1,
				Max:       10,
				Used:      20,
				Committed: 30,
			},
			{
				Type:      3,
				Init:      1,
				Max:       10,
				Used:      20,
				Committed: 30,
			},
			{
				Type:      5,
				Init:      1,
				Max:       10,
				Used:      20,
				Committed: 30,
			},
		},
		Gc: []*agentv3.GC{
			{
				Phase: 0,
				Count: 10,
				Time:  20,
			},
			{
				Phase: 1,
				Count: 30,
				Time:  40,
			},
		},
		Thread: &agentv3.Thread{
			LiveCount:                    1,
			DaemonCount:                  2,
			PeakCount:                    3,
			RunnableStateThreadCount:     4,
			BlockedStateThreadCount:      5,
			WaitingStateThreadCount:      6,
			TimedWaitingStateThreadCount: 7,
		},
	}
}

func TestConvertJvmMetrics(t *testing.T) {
	jvmMetrics := mockJvmMetrics()
	metrics := convertJvmMetrics(&agentv3.JVMMetricCollection{
		Metrics:         []*agentv3.JVMMetric{jvmMetrics},
		Service:         "service1",
		ServiceInstance: "instance1",
	}, "my-token")

	type Case struct {
		Metric string
		Val    float64
	}

	cases := []Case{
		{Metric: jvmGcYoungCount, Val: 10},
		{Metric: jvmGcYoungTime, Val: 20},
		{Metric: jvmGcOldCount, Val: 30},
		{Metric: jvmGcOldTime, Val: 40},
		{Metric: jvmMemoryHeapMax, Val: 10},
		{Metric: jvmMemoryHeapUsed, Val: 20},
		{Metric: jvmMemoryHeapCommitted, Val: 30},
		{Metric: jvmMemoryNoHeapInit, Val: 2},
		{Metric: jvmMemoryNoHeapMax, Val: 30},
		{Metric: jvmMemoryNoHeapUsed, Val: 40},
		{Metric: jvmMemoryNoHeapCommitted, Val: 50},
		{Metric: jvmMemoryCodeCacheInit, Val: 1},
		{Metric: jvmMemoryCodeCacheMax, Val: 10},
		{Metric: jvmMemoryCodeCacheUsed, Val: 20},
		{Metric: jvmMemoryCodeCacheCommitted, Val: 30},
		{Metric: jvmMemoryNewGenCommitted, Val: 30},
		{Metric: jvmMemoryOldGenCommitted, Val: 30},
		{Metric: jvmMemorySurvivorCommitted, Val: 30},
		{Metric: jvmMemoryMetaspaceInit, Val: 1},
		{Metric: jvmMemoryMetaspaceMax, Val: 10},
		{Metric: jvmMemoryMetaspaceUsed, Val: 20},
		{Metric: jvmMemoryMetaspaceCommitted, Val: 30},
		{Metric: jvmThreadLiveCount, Val: 1},
		{Metric: jvmThreadDaemonCount, Val: 2},
		{Metric: jvmThreadRunnableCount, Val: 4},
		{Metric: jvmThreadBlockedCount, Val: 5},
		{Metric: jvmThreadWaitingCount, Val: 6},
		{Metric: jvmThreadTimeWaitingCount, Val: 7},
	}

	n := 0
	foreach.Metrics(metrics.ResourceMetrics(), func(metric pmetric.Metric) {
		c := cases[n]
		assert.Equal(t, metric.Name(), c.Metric)
		assert.Equal(t, metric.Gauge().DataPoints().Len(), 1)
		assert.Equal(t, metric.Gauge().DataPoints().At(0).DoubleVal(), c.Val)
		n++
	})

	assert.Equal(t, 28, n)
}

func TestConvertNilJvmMetrics(t *testing.T) {
	metrics := convertJvmMetrics(&agentv3.JVMMetricCollection{
		Metrics:         []*agentv3.JVMMetric{nil},
		Service:         "service1",
		ServiceInstance: "instance1",
	}, "my-token")

	assert.Equal(t, 0, metrics.MetricCount())
	assert.Equal(t, 0, metrics.DataPointCount())
}
