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

	tests := []struct {
		metric string
		val    float64
	}{
		{metric: jvmGcYoungCount, val: 10},
		{metric: jvmGcYoungTime, val: 20},
		{metric: jvmGcOldCount, val: 30},
		{metric: jvmGcOldTime, val: 40},
		{metric: jvmMemoryHeapMax, val: 10},
		{metric: jvmMemoryHeapUsed, val: 20},
		{metric: jvmMemoryHeapCommitted, val: 30},
		{metric: jvmMemoryNoHeapInit, val: 2},
		{metric: jvmMemoryNoHeapMax, val: 30},
		{metric: jvmMemoryNoHeapUsed, val: 40},
		{metric: jvmMemoryNoHeapCommitted, val: 50},
		{metric: jvmMemoryCodeCacheInit, val: 1},
		{metric: jvmMemoryCodeCacheMax, val: 10},
		{metric: jvmMemoryCodeCacheUsed, val: 20},
		{metric: jvmMemoryCodeCacheCommitted, val: 30},
		{metric: jvmMemoryNewGenCommitted, val: 30},
		{metric: jvmMemoryOldGenCommitted, val: 30},
		{metric: jvmMemorySurvivorCommitted, val: 30},
		{metric: jvmMemoryMetaspaceInit, val: 1},
		{metric: jvmMemoryMetaspaceMax, val: 10},
		{metric: jvmMemoryMetaspaceUsed, val: 20},
		{metric: jvmMemoryMetaspaceCommitted, val: 30},
		{metric: jvmThreadLiveCount, val: 1},
		{metric: jvmThreadDaemonCount, val: 2},
		{metric: jvmThreadRunnableCount, val: 4},
		{metric: jvmThreadBlockedCount, val: 5},
		{metric: jvmThreadWaitingCount, val: 6},
		{metric: jvmThreadTimeWaitingCount, val: 7},
	}

	n := 0
	foreach.Metrics(metrics, func(metric pmetric.Metric) {
		c := tests[n]
		assert.Equal(t, metric.Name(), c.metric)
		assert.Equal(t, metric.Gauge().DataPoints().Len(), 1)
		assert.Equal(t, metric.Gauge().DataPoints().At(0).DoubleVal(), c.val)
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
