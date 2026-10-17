// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package v1beta3

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func partitionWorkerRequest() cmdb.SharedTopologyQuery {
	return cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "a", StartTime: 1700000000, EndTime: 1700003540, Step: "60s", MaxHops: 2}
}

func TestTopologyPartitionLeavesUseIdleWorkers(t *testing.T) {
	oldYolo := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = oldYolo })
	ctx, cancel := context.WithTimeout(initTimeGraphQueryTestEnvironment(), time.Second)
	defer cancel()
	records := partitionTestRecords{coverage: map[string]int{}}
	model := partitionTestModel(t, true, false, false, false, &records, nil)
	query := model.timeGraphVMQueryWithPartial
	var started atomic.Int32
	var once sync.Once
	var mu sync.Mutex
	both := make(chan struct{})
	model.timeGraphVMQueryWithPartial = func(ctx context.Context, ts *structured.QueryTs, expression string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, bool, error) {
		if timeGraphQueryStage(ctx) == "relation-edge" {
			partitioned := false
			for _, field := range ts.QueryList[0].Conditions.FieldList {
				partitioned = partitioned || field.Operator == structured.ConditionRegEqual || field.Operator == structured.ConditionNotRegEqual
			}
			if partitioned {
				if started.Add(1) >= 2 {
					once.Do(func() { close(both) })
				}
				select {
				case <-both:
				case <-ctx.Done():
					return nil, false, ctx.Err()
				}
			}
		}
		mu.Lock()
		defer mu.Unlock()
		return query(ctx, ts, expression, instant, start, end, step)
	}
	result, err := model.QuerySharedTopology(ctx, partitionWorkerRequest())
	require.NoError(t, err)
	require.GreaterOrEqual(t, started.Load(), int32(2))
	require.Len(t, result.Snapshots, 60)
	require.Equal(t, 930, records.points)
	for key, count := range records.coverage {
		require.Equal(t, 1, count, key)
	}
	require.Zero(t, len(topologyBackendSlots))
}

func TestTopologyPartitionWorkerFailureCancelsSiblings(t *testing.T) {
	for _, outcome := range []string{"backend", "apply", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			topologyAdmissionTestEnvironment(t, 1)
			yoloMode = true
			ctx, cancel := context.WithTimeout(initTimeGraphQueryTestEnvironment(), time.Second)
			defer cancel()
			records := partitionTestRecords{coverage: map[string]int{}}
			model := partitionTestModel(t, true, false, false, false, &records, nil)
			query := model.timeGraphVMQueryWithPartial
			var leaves, active atomic.Int32
			failure := errors.New("partition backend unavailable")
			model.timeGraphVMQueryWithPartial = func(ctx context.Context, ts *structured.QueryTs, expression string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, bool, error) {
				active.Add(1)
				defer active.Add(-1)
				partitioned := false
				for _, field := range ts.QueryList[0].Conditions.FieldList {
					partitioned = partitioned || field.Operator == structured.ConditionRegEqual || field.Operator == structured.ConditionNotRegEqual
				}
				if timeGraphQueryStage(ctx) == "relation-edge" && partitioned {
					if leaves.Add(1) == 2 {
						switch outcome {
						case "backend":
							return nil, false, failure
						case "apply":
							return contractMatrix(map[string]string{"a_id": "invalid"}, start.UnixMilli()), false, nil
						case "cancel":
							cancel()
						}
					}
					<-ctx.Done()
					return nil, false, ctx.Err()
				}
				return query(ctx, ts, expression, instant, start, end, step)
			}
			_, err := model.QuerySharedTopology(ctx, partitionWorkerRequest())
			switch outcome {
			case "backend":
				require.ErrorIs(t, err, failure)
			case "apply":
				require.ErrorContains(t, err, "b_id")
			case "cancel":
				require.ErrorIs(t, err, context.Canceled)
			}
			require.GreaterOrEqual(t, leaves.Load(), int32(2))
			require.Zero(t, active.Load())
			require.Zero(t, len(topologyBackendSlots))
			require.Equal(t, map[string]int64{"active": 0, "waiters": 0, "reserved": 0, "materialized": 0}, topologyAdmissionTestState())
		})
	}
}

func TestTopologyPartitionFanoutReducesRepeatedReads(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	var reference []string
	for _, limited := range []bool{false, true} {
		records := partitionTestRecords{responseLimit: 10000, coverage: map[string]int{}}
		model := partitionTestModel(t, limited, false, false, false, &records, nil)
		result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), partitionWorkerRequest())
		require.NoError(t, err)
		got := canonicalTopologyForTest(t, result.Snapshots)
		if !limited {
			reference = got
		} else {
			require.Equal(t, reference, got)
			require.Equal(t, 1, records.probes)
			require.Equal(t, 1, records.rejected)
			require.Equal(t, 5, records.fetches)
			require.Equal(t, 16, records.series)
		}
		require.Equal(t, 930, records.points)
		for key, count := range records.coverage {
			require.Equal(t, 1, count, key)
		}
	}
}

// Includes a response limit and the same late identity as the contract tests.
// A fixed delay isolates scheduling and avoided calls; it is not a VM model.
func BenchmarkTopologyPartitionWorkers(b *testing.B) {
	old := yoloMode
	yoloMode = true
	b.Cleanup(func() { yoloMode = old })
	records := partitionTestRecords{responseLimit: 10000, coverage: map[string]int{}}
	model := partitionTestModel(b, true, false, false, false, &records, nil)
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
			b.Fatalf("incomplete graph: %v", err)
		}
		for n, frame := range result.Snapshots {
			nodes := 16
			if n >= 30 {
				nodes = 17
			}
			if len(frame.Nodes) != nodes || len(frame.Edges) != (nodes-1)*2 || frame.Partial {
				b.Fatalf("incomplete frame %d", n)
			}
		}
	}
	b.ReportMetric(float64(records.fetches+records.probes)/float64(b.N), "fetches/op")
	b.ReportMetric(float64(records.rejected)/float64(b.N), "rejected/op")
	b.ReportMetric(float64(records.points)/float64(b.N), "points/op")
}
