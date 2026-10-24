// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package v1beta3

import (
	"context"
	"fmt"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// One root owns most physical series. Once separated, it cannot be split on
// a_id again; b_id can split it. Three light roots should stay together instead
// of filling the remaining fanout slots. A late root is absent from all probes.
func prepartitionSkewModel(t testing.TB, limited, underestimated bool, records *partitionTestRecords) *Model {
	model := partitionTestModel(t, false, false, false, false, records, nil)
	const start = int64(1700000000000)
	stamps := make([]int64, 60)
	for i := range stamps {
		stamps[i] = start + int64(i)*60000
	}
	data := pl.Matrix{}
	for i := 0; i < 64; i++ {
		data = append(data, contractMatrix(map[string]string{"a_id": "a-heavy", "b_id": fmt.Sprintf("id-%06b", i)}, stamps...)...)
	}
	for _, root := range []string{"x-light", "y-light", "z-light"} {
		data = append(data, contractMatrix(map[string]string{"a_id": root, "b_id": root + "-target"}, stamps...)...)
	}
	data = append(data, contractMatrix(map[string]string{"a_id": "雪\nlate", "b_id": "late-target"}, stamps[30:]...)...)
	model.timeGraphVMQueryWithPartial = func(ctx context.Context, ts *structured.QueryTs, _ string, _ bool, s, e time.Time, _ time.Duration) (pl.Matrix, bool, error) {
		records.mu.Lock()
		defer records.mu.Unlock()
		if timeGraphQueryStage(ctx) == "relation-partition" {
			records.probes++
			require.Equal(t, s, e)
			field := ts.QueryList[0].AggregateMethodList[0].Dimensions[0]
			weights := map[string]float64{}
			for _, series := range data {
				if !partitionTestMatches(t, ts, series.Metric) || series.Points[0].T != start {
					continue
				}
				copies := 1000.0
				if underestimated {
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
			return matrix, false, nil
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
		// The backend byte budget is independent of the planner's physical-
		// point estimate: 16 full-grid heavy relations fit, all 64 do not.
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
		return matrix, false, nil
	}
	return model
}

func TestTopologyPrepartitionSkewKeepsLightRootsTogether(t *testing.T) {
	oldYolo, oldPlan := yoloMode, SharedTopologyPlanCandidates
	yoloMode = true
	t.Cleanup(func() { yoloMode, SharedTopologyPlanCandidates = oldYolo, oldPlan })
	var expected []string
	for _, mode := range []string{"unlimited", "weighted", "underestimated"} {
		t.Run(mode, func(t *testing.T) {
			SharedTopologyPlanCandidates = mode != "unlimited"
			records := partitionTestRecords{coverage: map[string]int{}}
			model := prepartitionSkewModel(t, mode != "unlimited", mode == "underestimated", &records)
			result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), partitionWorkerRequest())
			require.NoError(t, err)
			got := canonicalTopologyForTest(t, result.Snapshots)
			if mode == "unlimited" {
				expected = got
			} else {
				require.Equal(t, expected, got)
			}
			require.Len(t, result.Snapshots, 60)
			require.Equal(t, 4050, records.points)
			require.Len(t, records.coverage, 4050)
			for key, count := range records.coverage {
				require.Equal(t, 1, count, key)
			}
			for _, frame := range result.Snapshots {
				require.False(t, frame.Partial)
			}
			t.Logf("%s probes=%d fetches=%d rejected=%d points=%d", mode, records.probes, records.fetches, records.rejected, records.points)
			switch mode {
			case "weighted":
				require.Zero(t, records.rejected)
				require.Equal(t, 2, records.probes, "root plus the heavy branch's next identity dimension")
				require.Equal(t, 5, records.fetches, "four heavy leaves and one combined light branch")
			case "underestimated":
				require.Positive(t, records.rejected, "small estimates must still fall back after actual size rejection")
				require.Equal(t, 2, records.probes, "reuse the prepared root partitions instead of probing a_id twice")
			}
		})
	}
}
