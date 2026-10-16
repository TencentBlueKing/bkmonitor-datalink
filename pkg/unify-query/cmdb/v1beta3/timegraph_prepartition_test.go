// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package v1beta3

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// Physical series counts deliberately exceed output identity counts: several
// source series can aggregate to one relation. A late identity is absent from
// every single-point probe. The independent byte model can reject any planned
// query, including when the estimator is inaccurate.
func prepartitionTestModel(t testing.TB, perRoot int, limited bool, probeMode string, records *partitionTestRecords) *Model {
	m := partitionTestModel(t, false, false, false, false, records, nil)
	const start = int64(1700000000000)
	data := pl.Matrix{}
	for root := 0; root < 2; root++ {
		for i := 0; i < perRoot; i++ {
			stamps := make([]int64, 60)
			for n := range stamps {
				stamps[n] = start + int64(n)*60000
			}
			data = append(data, contractMatrix(map[string]string{"a_id": fmt.Sprintf("root-%d", root), "b_id": fmt.Sprintf("id-%02d", i)}, stamps...)...)
		}
	}
	late := make([]int64, 30)
	for n := range late {
		late[n] = start + int64(n+30)*60000
	}
	data = append(data, contractMatrix(map[string]string{"a_id": "root-1", "b_id": "雪\nlate"}, late...)...)
	m.timeGraphVMQueryWithPartial = func(ctx context.Context, ts *structured.QueryTs, _ string, _ bool, s, e time.Time, _ time.Duration) (pl.Matrix, bool, error) {
		records.mu.Lock()
		defer records.mu.Unlock()
		if timeGraphQueryStage(ctx) == "relation-partition" {
			records.probes++
			require.Equal(t, s, e)
			if probeMode == "failed" {
				return nil, false, errors.New("optional probe unavailable")
			}
			if probeMode == "empty" {
				return nil, false, nil
			}
			field := ts.QueryList[0].AggregateMethodList[0].Dimensions[0]
			weights := map[string]float64{}
			for _, series := range data {
				if !partitionTestMatches(t, ts, series.Metric) || series.Points[0].T != start {
					continue
				}
				if probeMode == "partial" && series.Metric.Get("a_id") == "root-1" {
					continue
				}
				copies := 1000.0
				if probeMode == "underestimated" {
					copies = 1
				}
				weights[series.Metric.Get(field)] += copies
			}
			matrix := pl.Matrix{}
			for value, weight := range weights {
				series := contractMatrix(map[string]string{field: value}, start)[0]
				series.Points[0].V = weight
				matrix = append(matrix, series)
			}
			return matrix, probeMode == "partial", nil
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
		return matrix, probeMode == "data-partial", nil
	}
	return m
}

func TestTopologyPrepartitionPreservesFullResultAndFallbacks(t *testing.T) {
	oldYolo, oldPlan := yoloMode, SharedTopologyPlanCandidates
	yoloMode, SharedTopologyPlanCandidates = true, true
	t.Cleanup(func() { yoloMode, SharedTopologyPlanCandidates = oldYolo, oldPlan })
	var expected []string
	for _, mode := range []string{"unlimited", "weighted", "underestimated", "partial", "failed", "empty"} {
		t.Run(mode, func(t *testing.T) {
			records := partitionTestRecords{coverage: map[string]int{}}
			model := prepartitionTestModel(t, 32, mode != "unlimited", mode, &records)
			// Use the ordinary full-read path as the unlimited semantic oracle.
			SharedTopologyPlanCandidates = mode != "unlimited"
			result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), partitionWorkerRequest())
			require.NoError(t, err)
			got := canonicalTopologyForTest(t, result.Snapshots)
			if mode == "unlimited" {
				expected = got
			} else {
				require.Equal(t, expected, got)
			}
			require.Len(t, result.Snapshots, 60)
			require.Equal(t, 3870, records.points)
			for key, count := range records.coverage {
				require.Equal(t, 1, count, key)
			}
			for _, frame := range result.Snapshots {
				require.False(t, frame.Partial, "probe quality must not replace data quality")
			}
			if mode == "weighted" {
				require.Zero(t, records.rejected, "plan both root and heavy child branches before full reads")
				require.Equal(t, 3, records.probes)
				require.Equal(t, 4, records.fetches)
			}
			if mode == "underestimated" {
				require.Positive(t, records.rejected)
				require.Equal(t, 3, records.probes, "reuse the root's query plan when the estimate was low")
			}
			t.Logf("%s probes=%d fetches=%d rejected=%d points=%d", mode, records.probes, records.fetches, records.rejected, records.points)
		})
	}
}

func TestTopologyPrepartitionEligibilityAndSmallRead(t *testing.T) {
	oldYolo, oldPlan := yoloMode, SharedTopologyPlanCandidates
	t.Cleanup(func() { yoloMode, SharedTopologyPlanCandidates = oldYolo, oldPlan })
	for _, mode := range []string{"short", "normal", "planner-disabled", "small-long"} {
		t.Run(mode, func(t *testing.T) {
			yoloMode, SharedTopologyPlanCandidates = mode != "normal", mode != "planner-disabled"
			records := partitionTestRecords{coverage: map[string]int{}}
			model := prepartitionTestModel(t, 2, false, "weighted", &records)
			request := partitionWorkerRequest()
			if mode == "short" {
				request.EndTime = request.StartTime + 60
			}
			_, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
			require.NoError(t, err)
			require.Equal(t, 1, records.fetches)
			require.Zero(t, records.rejected)
			if mode == "small-long" {
				require.Equal(t, 1, records.probes)
			} else {
				require.Zero(t, records.probes)
			}
		})
	}
}

func TestTopologyPrepartitionCancelsWithoutDataReads(t *testing.T) {
	oldYolo, oldPlan := yoloMode, SharedTopologyPlanCandidates
	yoloMode, SharedTopologyPlanCandidates = true, true
	t.Cleanup(func() { yoloMode, SharedTopologyPlanCandidates = oldYolo, oldPlan })
	ctx, cancel := context.WithCancel(initTimeGraphQueryTestEnvironment())
	defer cancel()
	records := partitionTestRecords{coverage: map[string]int{}}
	model := prepartitionTestModel(t, 32, true, "weighted", &records)
	query := model.timeGraphVMQueryWithPartial
	var probes atomic.Int32
	model.timeGraphVMQueryWithPartial = func(ctx context.Context, ts *structured.QueryTs, expression string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, bool, error) {
		if timeGraphQueryStage(ctx) == "relation-partition" && probes.Add(1) >= 2 {
			cancel()
			return nil, false, ctx.Err()
		}
		return query(ctx, ts, expression, instant, start, end, step)
	}
	_, err := model.QuerySharedTopology(ctx, partitionWorkerRequest())
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, records.fetches)
	require.Zero(t, len(topologyBackendSlots))
}

func TestTopologyPrepartitionPreservesDataPartial(t *testing.T) {
	oldYolo, oldPlan := yoloMode, SharedTopologyPlanCandidates
	yoloMode, SharedTopologyPlanCandidates = true, true
	t.Cleanup(func() { yoloMode, SharedTopologyPlanCandidates = oldYolo, oldPlan })
	records := partitionTestRecords{coverage: map[string]int{}}
	model := prepartitionTestModel(t, 32, true, "data-partial", &records)
	result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), partitionWorkerRequest())
	require.NoError(t, err)
	require.Zero(t, records.rejected)
	require.Equal(t, 3870, records.points)
	for _, frame := range result.Snapshots {
		require.True(t, frame.Partial, "successful preflight must not hide partial data")
	}
}

func BenchmarkTopologyPrepartition(b *testing.B) {
	oldYolo, oldPlan := yoloMode, SharedTopologyPlanCandidates
	yoloMode, SharedTopologyPlanCandidates = true, true
	b.Cleanup(func() { yoloMode, SharedTopologyPlanCandidates = oldYolo, oldPlan })
	for _, perRoot := range []int{2, 32} {
		b.Run(fmt.Sprintf("relations-%d", 2*perRoot+1), func(b *testing.B) {
			records := partitionTestRecords{coverage: map[string]int{}}
			model := prepartitionTestModel(b, perRoot, true, "weighted", &records)
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
			b.ReportMetric(float64(records.rejected)/float64(b.N), "rejected/op")
			b.ReportMetric(float64(records.points)/float64(b.N), "points/op")
		})
	}
}
