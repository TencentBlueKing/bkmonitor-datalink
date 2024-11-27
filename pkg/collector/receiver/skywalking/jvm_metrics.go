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
	"go.opentelemetry.io/collector/pdata/pmetric"
	agentv3 "skywalking.apache.org/repo/goapi/collect/language/agent/v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/metricsbuilder"
)

const (
	jvmGcOldCount               = "jvm_gc_old_count"
	jvmGcOldTime                = "jvm_gc_old_time"
	jvmGcYoungCount             = "jvm_gc_young_count"
	jvmGcYoungTime              = "jvm_gc_young_time"
	jvmMemoryHeapMax            = "jvm_memory_heap_max"
	jvmMemoryHeapUsed           = "jvm_memory_heap_used"
	jvmMemoryHeapCommitted      = "jvm_memory_heap_committed"
	jvmMemoryNoHeapInit         = "jvm_memory_noheap_init"
	jvmMemoryNoHeapMax          = "jvm_memory_noheap_max"
	jvmMemoryNoHeapUsed         = "jvm_memory_noheap_used"
	jvmMemoryNoHeapCommitted    = "jvm_memory_noheap_committed"
	jvmMemoryCodeCacheInit      = "jvm_memory_codecache_init"
	jvmMemoryCodeCacheMax       = "jvm_memory_codecache_max"
	jvmMemoryCodeCacheUsed      = "jvm_memory_codecache_used"
	jvmMemoryCodeCacheCommitted = "jvm_memory_codecache_committed"
	jvmMemoryNewGenCommitted    = "jvm_memory_newgen_committed"
	jvmMemoryOldGenCommitted    = "jvm_memory_oldgen_committed"
	jvmMemorySurvivorCommitted  = "jvm_memory_survivor_committed"
	jvmMemoryMetaspaceInit      = "jvm_memory_metaspace_init"
	jvmMemoryMetaspaceMax       = "jvm_memory_metaspace_max"
	jvmMemoryMetaspaceUsed      = "jvm_memory_metaspace_used"
	jvmMemoryMetaspaceCommitted = "jvm_memory_metaspace_committed"
	jvmThreadLiveCount          = "jvm_thread_live_count"
	jvmThreadDaemonCount        = "jvm_thread_daemon_count"
	jvmThreadRunnableCount      = "jvm_thread_runnable_count"
	jvmThreadBlockedCount       = "jvm_thread_blocked_count"
	jvmThreadWaitingCount       = "jvm_thread_waiting_count"
	jvmThreadTimeWaitingCount   = "jvm_thread_time_waiting_count"
)

func convertJvmMetrics(segment *agentv3.JVMMetricCollection, token string) pmetric.Metrics {
	converter := &jvmMetricsConverter{mb: metricsbuilder.New(
		metricsbuilder.ResourceKv{Key: "service_name", Value: segment.GetService()},
		metricsbuilder.ResourceKv{Key: "bk_instance_id", Value: segment.GetServiceInstance()},
		metricsbuilder.ResourceKv{Key: "bk.data.token", Value: token},
	)}

	for _, jvmMetrics := range segment.Metrics {
		if jvmMetrics == nil {
			continue
		}
		converter.Convert(jvmMetrics)
	}

	return converter.mb.Get()
}

type jvmMetricsConverter struct {
	mb *metricsbuilder.Builder
}

func (c *jvmMetricsConverter) Convert(jvmMetric *agentv3.JVMMetric) {
	c.convertGcMetrics(jvmMetric)
	c.convertMemoryMetrics(jvmMetric)
	c.convertMemoryPoolMetrics(jvmMetric)
	c.convertThreadMetrics(jvmMetric)
}

func (c *jvmMetricsConverter) convertGcMetrics(jvmMetric *agentv3.JVMMetric) {
	ts := microsecondsToTimestamp(jvmMetric.GetTime())
	for _, m := range jvmMetric.Gc {
		switch m.Phase {
		case 0:
			c.mb.Build(jvmGcYoungCount, metricsbuilder.Metric{Val: float64(m.Count), Ts: ts})
			c.mb.Build(jvmGcYoungTime, metricsbuilder.Metric{Val: float64(m.Time), Ts: ts})
		case 1:
			c.mb.Build(jvmGcOldCount, metricsbuilder.Metric{Val: float64(m.Count), Ts: ts})
			c.mb.Build(jvmGcOldTime, metricsbuilder.Metric{Val: float64(m.Time), Ts: ts})
		}
	}
}

func (c *jvmMetricsConverter) convertMemoryMetrics(jvmMetric *agentv3.JVMMetric) {
	ts := microsecondsToTimestamp(jvmMetric.GetTime())
	for _, m := range jvmMetric.Memory {
		if m.IsHeap {
			c.mb.Build(jvmMemoryHeapMax, metricsbuilder.Metric{Val: float64(m.Max), Ts: ts})
			c.mb.Build(jvmMemoryHeapUsed, metricsbuilder.Metric{Val: float64(m.Used), Ts: ts})
			c.mb.Build(jvmMemoryHeapCommitted, metricsbuilder.Metric{Val: float64(m.Committed), Ts: ts})
		} else {
			c.mb.Build(jvmMemoryNoHeapInit, metricsbuilder.Metric{Val: float64(m.Init), Ts: ts})
			c.mb.Build(jvmMemoryNoHeapMax, metricsbuilder.Metric{Val: float64(m.Max), Ts: ts})
			c.mb.Build(jvmMemoryNoHeapUsed, metricsbuilder.Metric{Val: float64(m.Used), Ts: ts})
			c.mb.Build(jvmMemoryNoHeapCommitted, metricsbuilder.Metric{Val: float64(m.Committed), Ts: ts})
		}
	}
}

func (c *jvmMetricsConverter) convertMemoryPoolMetrics(jvmMetric *agentv3.JVMMetric) {
	ts := microsecondsToTimestamp(jvmMetric.GetTime())
	for _, m := range jvmMetric.MemoryPool {
		switch m.Type {
		case 0:
			c.mb.Build(jvmMemoryCodeCacheInit, metricsbuilder.Metric{Val: float64(m.Init), Ts: ts})
			c.mb.Build(jvmMemoryCodeCacheMax, metricsbuilder.Metric{Val: float64(m.Max), Ts: ts})
			c.mb.Build(jvmMemoryCodeCacheUsed, metricsbuilder.Metric{Val: float64(m.Used), Ts: ts})
			c.mb.Build(jvmMemoryCodeCacheCommitted, metricsbuilder.Metric{Val: float64(m.Committed), Ts: ts})
		case 1:
			c.mb.Build(jvmMemoryNewGenCommitted, metricsbuilder.Metric{Val: float64(m.Committed), Ts: ts})
		case 2:
			c.mb.Build(jvmMemoryOldGenCommitted, metricsbuilder.Metric{Val: float64(m.Committed), Ts: ts})
		case 3:
			c.mb.Build(jvmMemorySurvivorCommitted, metricsbuilder.Metric{Val: float64(m.Committed), Ts: ts})
		case 5:
			c.mb.Build(jvmMemoryMetaspaceInit, metricsbuilder.Metric{Val: float64(m.Init), Ts: ts})
			c.mb.Build(jvmMemoryMetaspaceMax, metricsbuilder.Metric{Val: float64(m.Max), Ts: ts})
			c.mb.Build(jvmMemoryMetaspaceUsed, metricsbuilder.Metric{Val: float64(m.Used), Ts: ts})
			c.mb.Build(jvmMemoryMetaspaceCommitted, metricsbuilder.Metric{Val: float64(m.Committed), Ts: ts})
		}
	}
}

func (c *jvmMetricsConverter) convertThreadMetrics(jvmMetric *agentv3.JVMMetric) {
	m := jvmMetric.Thread
	ts := microsecondsToTimestamp(jvmMetric.GetTime())

	c.mb.Build(jvmThreadLiveCount, metricsbuilder.Metric{Val: float64(m.LiveCount), Ts: ts})
	c.mb.Build(jvmThreadDaemonCount, metricsbuilder.Metric{Val: float64(m.DaemonCount), Ts: ts})
	c.mb.Build(jvmThreadRunnableCount, metricsbuilder.Metric{Val: float64(m.RunnableStateThreadCount), Ts: ts})
	c.mb.Build(jvmThreadBlockedCount, metricsbuilder.Metric{Val: float64(m.BlockedStateThreadCount), Ts: ts})
	c.mb.Build(jvmThreadWaitingCount, metricsbuilder.Metric{Val: float64(m.WaitingStateThreadCount), Ts: ts})
	c.mb.Build(jvmThreadTimeWaitingCount, metricsbuilder.Metric{Val: float64(m.TimedWaitingStateThreadCount), Ts: ts})
}
