// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.
package metric

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestTopologyInFlightBalancesSuccessfulAndFailedWork(t *testing.T) {
	for _, mode := range []string{"instant", "range"} {
		gauge := cmdbTopologyInFlight.WithLabelValues(mode)
		before := metricValue(t, gauge).GetGauge().GetValue()
		CMDBTopologyInFlightAdd(mode, 1)
		require.Equal(t, before+1, metricValue(t, gauge).GetGauge().GetValue())
		CMDBTopologyInFlightAdd(mode, -1)
		require.Equal(t, before, metricValue(t, gauge).GetGauge().GetValue())
	}
	before := timeGraphFamilies(t)
	CMDBTopologyInFlightAdd("private-mode", 1)
	CMDBTopologyInFlightAdd("instant", 2)
	CMDBTopologyInFlightAdd("range", -2)
	CMDBTopologyInFlightAdd("range", 0)
	require.Equal(t, before, timeGraphFamilies(t))
}

func TestTopologyAndMatrixSizeIncludeEveryDimension(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"instant", "range"} {
		for kind, count := range map[string]int{"points": 2, "nodes": 3, "edges": 5, "partial_snapshots": 7} {
			requireHistogramObservation(t, cmdbTopologySize.WithLabelValues(mode, kind), float64(count), func() {
				CMDBTopologySizeObserve(ctx, mode, 2, 3, 5, 7)
			})
			requireHistogramObservation(t, cmdbTopologySize.WithLabelValues(mode, kind), 0, func() {
				CMDBTopologySizeObserve(ctx, mode, 0, 0, 0, 0)
			})
		}
	}
	for _, storage := range []string{"shared", "time-buckets"} {
		for kind, count := range map[string]int{"logical": 3, "physical": 2, "reused": 1} {
			requireHistogramObservation(t, cmdbTimeGraphMatrixQueriesPerBuild.WithLabelValues(storage, kind, "failed"), float64(count), func() {
				CMDBTimeGraphMatrixQueriesObserve(ctx, storage, "failed", 3, 2, 1)
			})
			requireHistogramObservation(t, cmdbTimeGraphMatrixQueriesPerBuild.WithLabelValues(storage, kind, "failed"), 0, func() {
				CMDBTimeGraphMatrixQueriesObserve(ctx, storage, "failed", 0, 0, 0)
			})
		}
	}
}

func TestTimeGraphLoadCountsEveryReturnedDimension(t *testing.T) {
	ctx := context.Background()
	for _, stage := range []string{"source-info", "relation-edge", "relation-partition", "target-info"} {
		name := "load_dimension_contract"
		for kind, count := range map[string]int{"series": 2, "points": 3, "label_bytes": 5} {
			requireCounterIncrease(t, cmdbTimeGraphLoadSize.WithLabelValues(stage, name, kind), float64(count), func() {
				requireCounterIncrease(t, cmdbTimeGraphLoadOperations.WithLabelValues(stage, name, "partial"), 1, func() {
					requireCounterIncrease(t, cmdbTimeGraphLoadSeconds.WithLabelValues(stage, name, "partial"), 1, func() {
						CMDBTimeGraphLoadObserve(ctx, stage, name, "partial", time.Second, TimeGraphLoadSize{Returned: true, Series: 2, Points: 3, LabelBytes: 5})
					})
				})
			})
			requireCounterIncrease(t, cmdbTimeGraphLoadSize.WithLabelValues(stage, name, kind), 0, func() {
				requireCounterIncrease(t, cmdbTimeGraphLoadOperations.WithLabelValues(stage, name, "failed"), 1, func() {
					CMDBTimeGraphLoadObserve(ctx, stage, name, "failed", 0, TimeGraphLoadSize{Returned: false, Series: 2, Points: 3, LabelBytes: 5})
				})
			})
		}
		require.InDelta(t, time.Now().Unix(), metricValue(t, cmdbTimeGraphLoadLastTimestamp.WithLabelValues(stage, name)).GetGauge().GetValue(), 2)
	}
	requireCounterIncrease(t, cmdbTimeGraphMatrixReuses.WithLabelValues("relation-edge", "reused_relation"), 1, func() {
		CMDBTimeGraphMatrixReuse(ctx, "relation-edge", "reused_relation")
	})
	registry := prometheus.NewRegistry()
	registry.MustRegister(cmdbTimeGraphLoadSize, cmdbTimeGraphLoadSeconds, cmdbTimeGraphLoadOperations, cmdbTimeGraphLoadLastTimestamp, cmdbTimeGraphMatrixReuses)
	before, err := registry.Gather()
	require.NoError(t, err)
	for _, stage := range []string{"source-info", "target-info", "relation-partition", "private-stage"} {
		CMDBTimeGraphMatrixReuse(ctx, stage, "reused_relation")
	}
	for _, size := range []TimeGraphLoadSize{{Series: -1}, {Points: -1}, {LabelBytes: -1}} {
		CMDBTimeGraphLoadObserve(ctx, "source-info", "private-name", "success", time.Second, size)
	}
	CMDBTimeGraphLoadObserve(ctx, "private-stage", "private-name", "success", time.Second, TimeGraphLoadSize{})
	CMDBTimeGraphLoadObserve(ctx, "source-info", "private-name", "private-result", time.Second, TimeGraphLoadSize{})
	CMDBTimeGraphLoadObserve(ctx, "source-info", "private-name", "success", -time.Second, TimeGraphLoadSize{})
	after, err := registry.Gather()
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestTimeGraphMetricsRejectInvalidDimensions(t *testing.T) {
	ctx := context.Background()
	for name, record := range map[string]func(){
		"size-mode":        func() { CMDBTopologySizeObserve(ctx, "private-mode", 0, 0, 0, 0) },
		"size-nodes":       func() { CMDBTopologySizeObserve(ctx, "instant", 0, -1, 0, 0) },
		"size-edges":       func() { CMDBTopologySizeObserve(ctx, "instant", 0, 0, -1, 0) },
		"size-partial":     func() { CMDBTopologySizeObserve(ctx, "instant", 0, 0, 0, -1) },
		"reject-mode":      func() { CMDBTopologyRejectInc(ctx, "private-mode", "invalid_request") },
		"stage-result":     func() { CMDBTimeGraphStageObserve(ctx, "build", "private-result", time.Second) },
		"stage-duration":   func() { CMDBTimeGraphStageObserve(ctx, "build", "success", -time.Second) },
		"graph-stage":      func() { CMDBTimeGraphSizeObserve(ctx, "private-stage", "nodes", 1) },
		"graph-count":      func() { CMDBTimeGraphSizeObserve(ctx, "build", "nodes", -1) },
		"storage-result":   func() { CMDBTimeGraphStorageObserve(ctx, "shared", "nodes", "private-result", 1) },
		"storage-count":    func() { CMDBTimeGraphStorageObserve(ctx, "shared", "nodes", "success", -1) },
		"phase-storage":    func() { CMDBTimeGraphBuildPhaseObserve(ctx, "private-storage", "local", "success", time.Second) },
		"phase-result":     func() { CMDBTimeGraphBuildPhaseObserve(ctx, "shared", "local", "private-result", time.Second) },
		"phase-duration":   func() { CMDBTimeGraphBuildPhaseObserve(ctx, "shared", "local", "success", -time.Second) },
		"matrix-physical":  func() { CMDBTimeGraphMatrixQueriesObserve(ctx, "shared", "success", 0, -1, 0) },
		"matrix-reused":    func() { CMDBTimeGraphMatrixQueriesObserve(ctx, "shared", "success", 0, 0, -1) },
		"payload-result":   func() { CMDBTopologyPayloadObserve(ctx, "request-body", "private-result", 1) },
		"payload-negative": func() { CMDBTopologyPayloadObserve(ctx, "request-body", "success", -1) },
	} {
		t.Run(name, func(t *testing.T) {
			before := timeGraphFamilies(t)
			record()
			require.Equal(t, before, timeGraphFamilies(t))
		})
	}
}
