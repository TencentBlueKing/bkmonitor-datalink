// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"errors"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestSplitTimeGraphQueryRange(t *testing.T) {
	query := &structured.QueryTs{Start: "1700000000", End: "1700000007", Step: "1s"}
	left, right, ok := splitTimeGraphQueryRange(query)
	require.True(t, ok)
	require.Equal(t, "1700000000", left.Start)
	require.Equal(t, "1700000003", left.End)
	require.Equal(t, "1700000004", right.Start)
	require.Equal(t, query.End, right.End)
	require.Equal(t, "1700000007", query.End, "splitting must not mutate the parent query")

	leaf := &structured.QueryTs{Start: "1700000000", End: "1700000000", Step: "1s"}
	_, _, ok = splitTimeGraphQueryRange(leaf)
	require.False(t, ok)
	unaligned := &structured.QueryTs{Start: "1700000000", End: "1700000005", Step: "2s"}
	_, _, ok = splitTimeGraphQueryRange(unaligned)
	require.False(t, ok)
	instant := &structured.QueryTs{Start: "1700000000", End: "1700000007", Step: "1s", Instant: true}
	_, _, ok = splitTimeGraphQueryRange(instant)
	require.False(t, ok)
}

func TestSharedTopologyYoloSplitsOversizedVMResponses(t *testing.T) {
	oldYolo := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = oldYolo })
	const (
		start = int64(1700000000)
		count = 8
	)
	fields := []string{"source_middle_flow", "middle_target_flow"}
	labels := map[string]map[string]string{
		"source_middle_flow": {"source_id": "a", "middle_id": "b"},
		"middle_target_flow": {"middle_id": "b", "target_id": "c"},
	}
	request := cmdb.SharedTopologyQuery{
		SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"},
		StartTime: start, EndTime: start + count - 1, Step: "1s", MaxHops: 2,
	}
	newModel := func(failAbove int, alwaysFail bool, otherError bool, coverage map[string]map[int64]int, calls *int) *Model {
		model := sharedTopologyQueryModel(nil)
		model.timeGraphVMQuery = nil
		model.timeGraphVMQueryWithPartial = func(ctx context.Context, query *structured.QueryTs, _ string, _ bool, shardStart, shardEnd time.Time, step time.Duration) (pl.Matrix, bool, error) {
			*calls++
			require.True(t, metadata.GetQueryParams(ctx).IsSkipK8s, "shards must preserve query routing flags")
			field := query.QueryList[0].FieldName
			points := int(shardEnd.Sub(shardStart)/step) + 1
			if otherError {
				return nil, false, errors.New("backend unavailable")
			}
			if alwaysFail || points > failAbove {
				return nil, false, &metadata.BackendResponseTooLargeError{Message: "response body size exceeds test limit"}
			}
			timestamps := make([]int64, 0, points)
			for ts := shardStart; !ts.After(shardEnd); ts = ts.Add(step) {
				timestamps = append(timestamps, ts.UnixMilli())
				coverage[field][ts.Unix()]++
			}
			partial := field == "source_middle_flow" && shardStart.Unix() >= start+4
			return contractMatrix(labels[field], timestamps...), partial, nil
		}
		return model
	}

	baselineCalls := 0
	baselineCoverage := newCoverage(fields)
	baselineResult, err := newModel(count, false, false, baselineCoverage, &baselineCalls).QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
	require.NoError(t, err)
	require.Equal(t, len(fields), baselineCalls)

	splitCalls := 0
	splitCoverage := newCoverage(fields)
	splitResult, err := newModel(2, false, false, splitCoverage, &splitCalls).QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
	require.NoError(t, err)
	require.Greater(t, splitCalls, baselineCalls)
	require.Equal(t, canonicalTopologyIgnoringPartialForTest(t, baselineResult.Snapshots), canonicalTopologyIgnoringPartialForTest(t, splitResult.Snapshots))
	for _, field := range fields {
		require.Len(t, splitCoverage[field], count, field)
		for ts := start; ts < start+count; ts++ {
			require.Equal(t, 1, splitCoverage[field][ts], "%s timestamp %d must be applied exactly once", field, ts)
		}
	}
	for i, snapshot := range splitResult.Snapshots {
		require.Equal(t, i >= 4, snapshot.Partial, "partial status must remain limited to its successful shard")
		require.Len(t, snapshot.Nodes, 3)
		require.Len(t, snapshot.Edges, 2)
	}
}

func TestSharedTopologyResponseSizeSplitGates(t *testing.T) {
	request := cmdb.SharedTopologyQuery{
		SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"},
		StartTime: 1700000000, EndTime: 1700000007, Step: "1s", MaxHops: 2,
	}
	for _, test := range []struct {
		name       string
		yolo       bool
		alwaysFail bool
		otherError bool
		wantCalls  int
		wantErr    string
	}{
		{name: "default mode does not split", yolo: false, alwaysFail: true, wantCalls: 1, wantErr: "response body size exceeds"},
		{name: "non-size error does not retry", yolo: true, otherError: true, wantCalls: 1, wantErr: "backend unavailable"},
		{name: "single point leaf terminates", yolo: true, alwaysFail: true, wantCalls: 4, wantErr: "response body size exceeds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			oldYolo := yoloMode
			yoloMode = test.yolo
			t.Cleanup(func() { yoloMode = oldYolo })
			calls := 0
			model := sharedTopologyQueryModel(nil)
			model.timeGraphVMQuery = nil
			model.timeGraphVMQueryWithPartial = func(_ context.Context, _ *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, bool, error) {
				calls++
				if test.otherError {
					return nil, false, errors.New("backend unavailable")
				}
				return nil, false, &metadata.BackendResponseTooLargeError{Message: "response body size exceeds test limit"}
			}
			_, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
			require.ErrorContains(t, err, test.wantErr)
			require.Equal(t, test.wantCalls, calls)
		})
	}
}

func newCoverage(fields []string) map[string]map[int64]int {
	coverage := make(map[string]map[int64]int, len(fields))
	for _, field := range fields {
		coverage[field] = make(map[int64]int)
	}
	return coverage
}

func canonicalTopologyIgnoringPartialForTest(t *testing.T, snapshots []cmdb.SharedTopologySnapshot) []string {
	t.Helper()
	copyOfSnapshots := make([]cmdb.SharedTopologySnapshot, len(snapshots))
	copy(copyOfSnapshots, snapshots)
	for i := range copyOfSnapshots {
		copyOfSnapshots[i].Partial = false
		copyOfSnapshots[i].PartialReason = ""
	}
	return canonicalTopologyForTest(t, copyOfSnapshots)
}
