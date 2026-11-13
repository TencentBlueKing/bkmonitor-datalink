// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.
package metric

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func metricValue(t *testing.T, metric prometheus.Metric) *dto.Metric {
	t.Helper()
	value := &dto.Metric{}
	require.NoError(t, metric.Write(value))
	return value
}

func requireCounterIncrease(t *testing.T, counter prometheus.Counter, amount float64, record func()) {
	t.Helper()
	before := metricValue(t, counter).GetCounter().GetValue()
	record()
	require.Equal(t, before+amount, metricValue(t, counter).GetCounter().GetValue())
}

func requireHistogramObservation(t *testing.T, observer prometheus.Observer, amount float64, record func()) {
	t.Helper()
	metric := observer.(prometheus.Metric)
	before := metricValue(t, metric).GetHistogram()
	record()
	after := metricValue(t, metric).GetHistogram()
	require.Equal(t, before.GetSampleCount()+1, after.GetSampleCount())
	require.InDelta(t, before.GetSampleSum()+amount, after.GetSampleSum(), 0.000001)
}

func TestTopologyMetricsPreserveSupportedLabels(t *testing.T) {
	ctx := context.Background()
	for _, scope := range []string{"request", "query"} {
		for _, mode := range []string{"instant", "range"} {
			for _, result := range []string{"success", "empty", "partial", "failed", "rejected", "canceled", "timeout"} {
				t.Run(scope+"/"+mode+"/"+result, func(t *testing.T) {
					counter := cmdbTopologyOperationsTotal.WithLabelValues(scope, mode, result)
					requireCounterIncrease(t, counter, 1, func() {
						requireHistogramObservation(t, cmdbTopologyOperationSeconds.WithLabelValues(scope, mode), 0, func() {
							CMDBTopologyObserve(ctx, scope, mode, result, 0)
						})
					})
				})
			}
		}
	}
	for _, stage := range []string{
		"build", "source-info", "relation-edge", "target-info", "topology-traversal",
		"topology-state", "topology-materialize", "topology-convert", "topology-encode", "apply-relation",
		"apply-source-info", "apply-target-info", "matrix-validation", "topology-propagation", "cleanup", "admission", "admission-release",
	} {
		t.Run(stage, func(t *testing.T) {
			requireHistogramObservation(t, cmdbTimeGraphStageSeconds.WithLabelValues(stage, "success"), 1, func() {
				CMDBTimeGraphStageObserve(ctx, stage, "success", time.Second)
			})
		})
	}
	for _, stage := range []string{"source-info", "relation-edge", "target-info"} {
		for _, kind := range []string{"series", "points"} {
			t.Run(stage+"/"+kind, func(t *testing.T) {
				requireHistogramObservation(t, cmdbTimeGraphSize.WithLabelValues(stage, kind), 2, func() {
					CMDBTimeGraphSizeObserve(ctx, stage, kind, 2)
				})
			})
		}
	}
}

func TestTopologyReasonMetricsKeepKnownReasonsAndDeduplicate(t *testing.T) {
	ctx := context.Background()
	for _, reason := range []string{
		"backend_partial", "vm_storage_coverage_mismatch", "relation_no_physical_route", "source_info_no_route",
		"target_info_failed", "max_response_bytes", "max_shared_topology_points", "other",
	} {
		t.Run("quality/"+reason, func(t *testing.T) {
			counter := cmdbRelationQualityTotal.WithLabelValues("compat", "instant", "partial", reason)
			requireCounterIncrease(t, counter, 1, func() {
				CMDBRelationQualityInc(ctx, "compat", "instant", "partial", reason+":detail;"+reason+":second")
			})
		})
	}
	requireCounterIncrease(t, cmdbRelationQualityTotal.WithLabelValues("compat", "range", "partial", "other"), 1, func() {
		CMDBRelationQualityInc(ctx, "compat", "range", "partial", "unknown-one:private;unknown-two:private")
	})
	// VM 实例追加原因时用 "; " 连接：去掉空格后各原因计入自己的类别，不落进 other。
	requireCounterIncrease(t, cmdbRelationQualityTotal.WithLabelValues("compat", "range", "partial", "backend_partial"), 1, func() {
		requireCounterIncrease(t, cmdbRelationQualityTotal.WithLabelValues("compat", "range", "partial", "vm_storage_coverage_mismatch"), 1, func() {
			requireCounterIncrease(t, cmdbRelationQualityTotal.WithLabelValues("compat", "range", "partial", "other"), 0, func() {
				CMDBRelationQualityInc(ctx, "compat", "range", "partial", "backend_partial; vm_storage_coverage_mismatch:storage-a")
			})
		})
	})
	// 两个入口的同名原因各计各的。
	requireCounterIncrease(t, cmdbRelationQualityTotal.WithLabelValues("topology", "range", "partial", "relation_no_physical_route"), 1, func() {
		requireCounterIncrease(t, cmdbRelationQualityTotal.WithLabelValues("compat", "range", "partial", "relation_no_physical_route"), 0, func() {
			CMDBRelationQualityInc(ctx, "topology", "range", "partial", "relation_no_physical_route:metric_a")
		})
	})
	for _, reason := range []string{
		"invalid_request", "max_shared_topology_points", "max_response_bytes", "max_topology_queries", "max_topology_request_bytes",
		"max_topology_sources",
	} {
		t.Run("rejection/"+reason, func(t *testing.T) {
			requireCounterIncrease(t, cmdbTopologyRejectionsTotal.WithLabelValues("range", reason), 1, func() {
				CMDBTopologyRejectInc(ctx, "range", reason)
			})
		})
	}
	// 取数回退只有固定原因，其余归入 other。
	requireCounterIncrease(t, cmdbTopologyFetchFallbackTotal.WithLabelValues("selector_not_eligible"), 1, func() {
		CMDBTopologyFetchFallbackInc(ctx, "selector_not_eligible")
	})
	requireCounterIncrease(t, cmdbTopologyFetchFallbackTotal.WithLabelValues("other"), 1, func() {
		CMDBTopologyFetchFallbackInc(ctx, "pod-1 is not eligible")
	})
	// 内部规模不再是拒绝原因，旧原因名不占独立标签。
	for _, reason := range []string{
		"max_targets", "max_graph_nodes", "max_graph_edges", "max_graph_node_infos", "max_topology_output_bytes",
		"max_topology_admission_waiters", "max_topology_reserved_bytes", "max_topology_memory_headroom",
	} {
		t.Run("removed/"+reason, func(t *testing.T) {
			requireCounterIncrease(t, cmdbTopologyRejectionsTotal.WithLabelValues("range", "other"), 1, func() {
				CMDBTopologyRejectInc(ctx, "range", reason)
			})
			requireCounterIncrease(t, cmdbRelationQualityTotal.WithLabelValues("compat", "range", "truncated", "other"), 1, func() {
				CMDBRelationQualityInc(ctx, "compat", "range", "truncated", reason)
			})
		})
	}
}

func TestTimeGraphStorageMetricsPreserveSupportedLabels(t *testing.T) {
	ctx := context.Background()
	for _, storage := range []string{"shared", "time-buckets"} {
		for _, kind := range []string{"nodes", "relations", "attribute_versions", "legacy_graphs", "edge_time_entries", "node_time_entries"} {
			t.Run(storage+"/"+kind, func(t *testing.T) {
				requireHistogramObservation(t, cmdbTimeGraphStorageSize.WithLabelValues(storage, kind, "success"), 0, func() {
					CMDBTimeGraphStorageObserve(ctx, storage, kind, "success", 0)
				})
			})
		}
		for _, phase := range []string{"matrix", "local"} {
			t.Run(storage+"/"+phase, func(t *testing.T) {
				requireHistogramObservation(t, cmdbTimeGraphBuildPhaseSeconds.WithLabelValues(storage, phase, "failed"), 1, func() {
					CMDBTimeGraphBuildPhaseObserve(ctx, storage, phase, "failed", time.Second)
				})
			})
		}
	}
	for _, stage := range []string{"request-body", "backend-response", "response-item"} {
		t.Run(stage, func(t *testing.T) {
			requireHistogramObservation(t, cmdbTopologyPayloadBytes.WithLabelValues(stage, "partial"), 17, func() {
				CMDBTopologyPayloadObserve(ctx, stage, "partial", 17)
			})
		})
	}
}

func TestVMRetryMetricsPreserveBoundedEvents(t *testing.T) {
	ctx := context.Background()
	for _, event := range []string{"attempted", "recovered", "failed", "canceled"} {
		for _, reason := range []string{"client_conn_close", "goaway_no_error", "write_broken_pipe"} {
			t.Run(event+"/"+reason, func(t *testing.T) {
				counter := vmQuerySyncWriteRetryEvents.WithLabelValues(event)
				if reason != "write_broken_pipe" {
					counter = vmQuerySyncHTTP2RetryEvents.WithLabelValues(event, reason)
				}
				requireCounterIncrease(t, counter, 1, func() { VMQuerySyncRetryInc(ctx, event, reason) })
			})
		}
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(vmQuerySyncHTTP2RetryEvents, vmQuerySyncWriteRetryEvents)
	before, err := registry.Gather()
	require.NoError(t, err)
	VMQuerySyncRetryInc(ctx, "private-event", "goaway_no_error")
	VMQuerySyncRetryInc(ctx, "attempted", "private-reason")
	after, err := registry.Gather()
	require.NoError(t, err)
	require.Equal(t, before, after)
}
