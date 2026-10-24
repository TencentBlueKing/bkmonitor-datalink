// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package v1beta3

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

type prepartitionJointStats struct {
	jointCalls      atomic.Int32
	probeRows       atomic.Int64
	probeLabelBytes atomic.Int64
}

func prepartitionJointModel(t testing.TB, limited bool, mode string, records *partitionTestRecords, stats *prepartitionJointStats) *Model {
	provider := selectorProvider()
	provider.resources = []ResourceType{"a", "b"}
	provider.primary = map[ResourceType][]string{"a": {"a_id"}, "b": {"b_id", "c_id"}}
	provider.fields = map[ResourceType][]string{}
	provider.schemas = []RelationSchema{{RelationType: "ab", Category: RelationCategoryStatic, FromType: "a", ToType: "b", MetricName: "ab_relation"}}
	model := &Model{schemaProvider: provider, timeGraphQueryReference: selectorQueryReference}
	const start = int64(1700000000000)
	stamps := make([]int64, 60)
	for i := range stamps {
		stamps[i] = start + int64(i)*60000
	}
	data := pl.Matrix{}
	for b := 0; b < 4; b++ {
		for c := 0; c < 32; c++ {
			value := fmt.Sprintf("%05b", c)
			if mode == "empty-value" && c == 0 {
				value = ""
			}
			data = append(data, contractMatrix(map[string]string{"a_id": "a-heavy", "b_id": fmt.Sprintf("%02b", b), "c_id": value}, stamps...)...)
		}
	}
	for _, root := range []string{"x-light", "y-light", "z-light"} {
		data = append(data, contractMatrix(map[string]string{"a_id": root, "b_id": root, "c_id": "light"}, stamps...)...)
	}
	data = append(data, contractMatrix(map[string]string{"a_id": "雪\nlate", "b_id": "late", "c_id": "雪\nlate"}, stamps[30:]...)...)
	data = append(data, contractMatrix(map[string]string{"a_id": "a-heavy", "b_id": "00", "c_id": "雪\nlate-second"}, stamps[30:]...)...)
	model.timeGraphVMQueryWithPartial = func(ctx context.Context, ts *structured.QueryTs, _ string, _ bool, s, e time.Time, _ time.Duration) (pl.Matrix, bool, error) {
		records.mu.Lock()
		defer records.mu.Unlock()
		if timeGraphQueryStage(ctx) == "relation-partition" {
			records.probes++
			require.Equal(t, s, e)
			fields := ts.QueryList[0].AggregateMethodList[0].Dimensions
			joint := len(fields) == 2
			if joint {
				stats.jointCalls.Add(1)
				require.Equal(t, structured.Dimensions{"b_id", "c_id"}, fields, "only the heavy child's next two dimensions are combined")
				switch mode {
				case "failed":
					return nil, false, errors.New("optional joint probe unavailable")
				case "oversize-probe":
					return nil, false, metadata.ErrBackendResponseTooLarge
				case "empty":
					return nil, false, nil
				}
			} else {
				require.Len(t, fields, 1)
			}
			weights := map[string]float64{}
			identities := map[string]map[string]string{}
			for _, series := range data {
				if !partitionTestMatches(t, ts, series.Metric) || series.Points[0].T != start {
					continue
				}
				labels := map[string]string{}
				key := ""
				for _, field := range fields {
					value := series.Metric.Get(field)
					labels[field] = value
					key += fmt.Sprintf("%d:%s", len(value), value)
				}
				copies := 1000.0
				if mode == "underestimated" || joint && (mode == "joint-low" || mode == "light-underestimated" && series.Metric.Get("b_id") == "00") {
					copies = 1
				}
				weights[key] += copies
				identities[key] = labels
			}
			matrix := pl.Matrix{}
			for key, weight := range weights {
				if joint && mode == "missing-field" {
					delete(identities[key], fields[1])
				}
				series := contractMatrix(identities[key], start)[0]
				series.Points[0].V = weight
				matrix = append(matrix, series)
			}
			if joint && mode == "invalid-value" {
				matrix[0].Points[0].V = math.NaN()
			}
			if joint && mode == "duplicate-tuple" {
				matrix = append(matrix, matrix[0])
			}
			stats.probeRows.Add(int64(len(matrix)))
			for _, series := range matrix {
				for _, label := range series.Metric {
					stats.probeLabelBytes.Add(int64(len(label.Name) + len(label.Value)))
				}
			}
			return matrix, joint && mode == "partial", nil
		}
		records.fetches++
		matrix := pl.Matrix{}
		points := 0
		for _, series := range data {
			if !partitionTestMatches(t, ts, series.Metric) {
				continue
			}
			selected := series
			selected.Points = nil
			for _, point := range series.Points {
				if point.T >= s.UnixMilli() && point.T <= e.UnixMilli() {
					selected.Points = append(selected.Points, point)
				}
			}
			if len(selected.Points) > 0 {
				matrix = append(matrix, selected)
				points += len(selected.Points)
			}
		}
		if limited && len(matrix)*20+points*10 > 11000 {
			records.rejected++
			return nil, false, metadata.ErrBackendResponseTooLarge
		}
		records.series += len(matrix)
		records.points += points
		for _, series := range matrix {
			for _, point := range series.Points {
				records.coverage[fmt.Sprintf("%s/%d", series.Metric.String(), point.T)]++
			}
		}
		return matrix, mode == "data-partial", nil
	}
	return model
}

func TestTopologyPrepartitionJointReusesHeavyBranchSample(t *testing.T) {
	oldYolo, oldPlan := yoloMode, SharedTopologyPlanCandidates
	yoloMode = true
	t.Cleanup(func() { yoloMode, SharedTopologyPlanCandidates = oldYolo, oldPlan })
	var expected []string
	for _, mode := range []string{"unlimited", "weighted", "failed", "oversize-probe", "empty", "partial", "missing-field", "duplicate-tuple", "invalid-value", "underestimated", "joint-low", "light-underestimated", "empty-value"} {
		t.Run(mode, func(t *testing.T) {
			SharedTopologyPlanCandidates = mode != "unlimited"
			records := partitionTestRecords{coverage: map[string]int{}}
			stats := &prepartitionJointStats{}
			want := expected
			wantPoints := 7920
			var wantCoverage map[string]int
			if mode == "empty-value" {
				SharedTopologyPlanCandidates = false
				oracleRecords := partitionTestRecords{coverage: map[string]int{}}
				oracle := prepartitionJointModel(t, false, mode, &oracleRecords, &prepartitionJointStats{})
				result, err := oracle.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), partitionWorkerRequest())
				require.NoError(t, err)
				want = canonicalTopologyForTest(t, result.Snapshots)
				// The original primary-key guard excludes four empty c_id
				// series; partitioning must preserve that filter exactly.
				require.Equal(t, 7680, oracleRecords.points)
				wantPoints, wantCoverage = oracleRecords.points, oracleRecords.coverage
				SharedTopologyPlanCandidates = true
			}
			model := prepartitionJointModel(t, mode != "unlimited", mode, &records, stats)
			result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), partitionWorkerRequest())
			require.NoError(t, err)
			got := canonicalTopologyForTest(t, result.Snapshots)
			if mode == "unlimited" {
				expected = got
			} else {
				require.Equal(t, want, got)
			}
			require.Len(t, result.Snapshots, 60)
			require.Equal(t, wantPoints, records.points)
			require.Len(t, records.coverage, wantPoints)
			if wantCoverage != nil {
				require.Equal(t, wantCoverage, records.coverage)
			}
			for key, count := range records.coverage {
				require.Equal(t, 1, count, key)
			}
			for _, frame := range result.Snapshots {
				require.False(t, frame.Partial, "probe quality is not graph quality")
			}
			t.Logf("%s probes=%d joint=%d fetches=%d rejected=%d points=%d probe_rows=%d probe_label_bytes=%d", mode, records.probes, stats.jointCalls.Load(), records.fetches, records.rejected, records.points, stats.probeRows.Load(), stats.probeLabelBytes.Load())
			switch mode {
			case "unlimited":
				require.Zero(t, stats.jointCalls.Load())
			case "weighted", "empty-value":
				require.Zero(t, records.rejected)
				require.Equal(t, 9, records.fetches)
				require.Equal(t, 2, records.probes, "root single-dimension probe plus one joint heavy-branch probe")
				require.Equal(t, int32(1), stats.jointCalls.Load())
			case "joint-low":
				require.Equal(t, 5, records.rejected)
				require.Equal(t, 6, records.probes, "reuse the joint first-dimension plan, then retain the next field for size fallback")
				require.Equal(t, int32(1), stats.jointCalls.Load())
			case "light-underestimated":
				require.Equal(t, 1, records.rejected)
				require.Equal(t, 3, records.probes, "the light leaf must retain its unused second dimension")
				require.Equal(t, int32(1), stats.jointCalls.Load())
			case "underestimated":
				require.Positive(t, records.rejected)
				require.Zero(t, stats.jointCalls.Load(), "actual size rejection retains the single-dimension fallback")
			default:
				require.Zero(t, records.rejected)
				require.Equal(t, 9, records.fetches)
				require.Equal(t, 7, records.probes, "one rejected optional joint probe plus the six original probes")
				require.Equal(t, int32(1), stats.jointCalls.Load())
			}
		})
	}
}

func TestTopologyPrepartitionJointCancellationAndDataQuality(t *testing.T) {
	oldYolo, oldPlan := yoloMode, SharedTopologyPlanCandidates
	yoloMode, SharedTopologyPlanCandidates = true, true
	t.Cleanup(func() { yoloMode, SharedTopologyPlanCandidates = oldYolo, oldPlan })
	for _, mode := range []string{"cancel-probe", "cancel-after-probe", "data-partial"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(initTimeGraphQueryTestEnvironment())
			defer cancel()
			records := partitionTestRecords{coverage: map[string]int{}}
			stats := &prepartitionJointStats{}
			model := prepartitionJointModel(t, true, mode, &records, stats)
			query := model.timeGraphVMQueryWithPartial
			model.timeGraphVMQueryWithPartial = func(ctx context.Context, ts *structured.QueryTs, expression string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, bool, error) {
				joint := timeGraphQueryStage(ctx) == "relation-partition" && len(ts.QueryList[0].AggregateMethodList[0].Dimensions) == 2
				if joint && mode == "cancel-probe" {
					cancel()
					return nil, false, ctx.Err()
				}
				matrix, partial, err := query(ctx, ts, expression, instant, start, end, step)
				if joint && mode == "cancel-after-probe" {
					cancel()
				}
				return matrix, partial, err
			}
			result, err := model.QuerySharedTopology(ctx, partitionWorkerRequest())
			if mode == "data-partial" {
				require.NoError(t, err)
				require.Len(t, result.Snapshots, 60)
				require.Equal(t, 7920, records.points)
				for _, frame := range result.Snapshots {
					require.True(t, frame.Partial)
				}
			} else {
				require.ErrorIs(t, err, context.Canceled)
			}
			require.Zero(t, len(topologyBackendSlots))
		})
	}
}

func BenchmarkTopologyPrepartitionJoint(b *testing.B) {
	oldYolo, oldPlan := yoloMode, SharedTopologyPlanCandidates
	yoloMode, SharedTopologyPlanCandidates = true, true
	b.Cleanup(func() { yoloMode, SharedTopologyPlanCandidates = oldYolo, oldPlan })
	records := partitionTestRecords{coverage: map[string]int{}}
	stats := &prepartitionJointStats{}
	model := prepartitionJointModel(b, true, "weighted", &records, stats)
	query := model.timeGraphVMQueryWithPartial
	model.timeGraphVMQueryWithPartial = func(ctx context.Context, ts *structured.QueryTs, expression string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, bool, error) {
		time.Sleep(5 * time.Millisecond)
		return query(ctx, ts, expression, instant, start, end, step)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), partitionWorkerRequest())
		if err != nil || len(result.Snapshots) != 60 {
			b.Fatalf("incomplete result: %v", err)
		}
	}
	b.ReportMetric(float64(records.fetches+records.probes)/float64(b.N), "calls/op")
	b.ReportMetric(float64(records.probes)/float64(b.N), "probes/op")
	b.ReportMetric(float64(stats.probeRows.Load())/float64(b.N), "probe-rows/op")
	b.ReportMetric(float64(stats.probeLabelBytes.Load())/float64(b.N), "probe-label-bytes/op")
	b.ReportMetric(float64(records.points)/float64(b.N), "points/op")
}
