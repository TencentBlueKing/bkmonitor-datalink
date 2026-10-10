// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestMatrixReuseSlot(t *testing.T) {
	ctx := withTimeGraphQueryStage(initTimeGraphQueryTestEnvironment(), "relation-edge")
	params := metadata.GetQueryParams(ctx).SetStorageType(metadata.VictoriaMetricsStorageType)
	loader := &timeGraphMatrixLoader{topology: true}
	query := &structured.QueryTs{}
	matrix := contractMatrix(map[string]string{"id": "a"}, 1000)
	calls := 0
	fetch := func() (pl.Matrix, bool, error) { calls++; return matrix, true, nil }
	first, partial, err := loader.loadPrepared(ctx, query, "a", params, fetch)
	require.NoError(t, err)
	require.True(t, partial)
	second, partial, err := loader.loadPrepared(ctx, query, "a", params, fetch)
	require.NoError(t, err)
	require.True(t, partial)
	require.Equal(t, first, second)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, loader.reusedQueryCount)
	_, _, err = loader.loadPrepared(ctx, query, "b", params, fetch)
	require.NoError(t, err)
	_, _, err = loader.loadPrepared(ctx, query, "a", params, fetch)
	require.NoError(t, err)
	require.Equal(t, 3, calls, "one-slot cache must evict previous query")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, _, err = loader.loadPrepared(canceled, query, "a", params, fetch)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 3, calls)
	failure := errors.New("backend unavailable")
	for i := 0; i < 2; i++ {
		_, _, err = loader.loadPrepared(ctx, query, "error", params, func() (pl.Matrix, bool, error) { calls++; return nil, false, failure })
		require.ErrorIs(t, err, failure)
	}
	require.Equal(t, 5, calls)
	for i := 0; i < 2; i++ {
		_, _, err = loader.loadPrepared(ctx, query, "empty", params, func() (pl.Matrix, bool, error) { calls++; return nil, false, nil })
		require.NoError(t, err)
	}
	require.Equal(t, 6, calls, "empty results are reusable")
}

func TestMatrixReusePreparedIdentity(t *testing.T) {
	ctx := initTimeGraphQueryTestEnvironment()
	loader := &timeGraphMatrixLoader{topology: true}
	params := metadata.GetQueryParams(ctx).SetStorageType(metadata.VictoriaMetricsStorageType)
	query := &structured.QueryTs{}
	key := loader.matrixReuseKey(ctx, query, "metric", params)
	require.NotEmpty(t, key)
	for _, changed := range []context.Context{metadata.WithExactTimeGrid(ctx), metadata.WithLeftOpenTimeWindow(ctx), metadata.WithBackendResponseLimit(ctx, 123)} {
		require.NotEqual(t, key, loader.matrixReuseKey(changed, query, "metric", params))
	}
	require.NotEqual(t, key, loader.matrixReuseKey(ctx, query, "other", params))
	params.Step = time.Second
	require.NotEqual(t, key, loader.matrixReuseKey(ctx, query, "metric", params))
	params.Step = 0
	metadata.SetUser(ctx, &metadata.User{TenantID: "other"})
	require.NotEqual(t, key, loader.matrixReuseKey(ctx, query, "metric", params))
}

func TestMatrixReuseDirectionsKeepAnswerAndInput(t *testing.T) {
	old := SharedTopologyReuseMatrix
	t.Cleanup(func() { SharedTopologyReuseMatrix = old })
	responses := map[string]pl.Matrix{
		"source_info_relation": contractMatrix(map[string]string{"source_id": "a"}, 1700000000000),
		"source_middle_flow":   contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, 1700000000000),
		"middle_target_flow":   nil,
	}
	before, err := json.Marshal(responses)
	require.NoError(t, err)
	var results []cmdb.SharedTopologyResult
	var counts []int
	for _, enabled := range []bool{false, true} {
		SharedTopologyReuseMatrix = enabled
		ctx := initTimeGraphQueryTestEnvironment()
		model := sharedTopologyQueryModel(responses)
		provider := sharedTopologyQueryProvider().(contractSchemaProvider)
		provider.schemas[0].IsDirectional = false
		model.schemaProvider = provider
		model.timeGraphQueryReference = func(ctx context.Context, query *structured.QueryTs) (metadata.QueryReference, error) {
			ref, err := timeGraphTestQueryReference(ctx, query)
			metadata.GetQueryParams(ctx).SetStorageType(metadata.VictoriaMetricsStorageType)
			return ref, err
		}
		calls := 0
		fetch := model.timeGraphVMQuery
		model.timeGraphVMQuery = func(ctx context.Context, query *structured.QueryTs, expr string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
			calls++
			return fetch(ctx, query, expr, instant, start, end, step)
		}
		result, err := model.QuerySharedTopology(ctx, cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, Timestamp: 1700000000, MaxHops: 2})
		require.NoError(t, err)
		results = append(results, result)
		counts = append(counts, calls)
		oldLimit := MaxSharedTopologyMatrixPoints
		MaxSharedTopologyMatrixPoints = 2
		_, budgetErr := model.QuerySharedTopology(ctx, cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, Timestamp: 1700000000, MaxHops: 2})
		MaxSharedTopologyMatrixPoints = oldLimit
		require.ErrorContains(t, budgetErr, "max_topology_matrix_points", "reuse must charge both logical applications")
	}
	require.Equal(t, results[0], results[1])
	require.Equal(t, counts[0]-1, counts[1])
	after, err := json.Marshal(responses)
	require.NoError(t, err)
	require.Equal(t, before, after, "apply must not mutate a reused Matrix")
}

func TestMatrixReuseExceptionalTopologyParity(t *testing.T) {
	previous := SharedTopologyReuseMatrix
	t.Cleanup(func() { SharedTopologyReuseMatrix = previous })
	for _, tt := range []struct {
		name        string
		matrix      pl.Matrix
		partial     bool
		failure     error
		wantPartial bool
	}{
		{name: "complete empty"},
		{name: "partial empty", partial: true, wantPartial: true},
		{name: "partial with relation", matrix: contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, 1700000000000, 1700000060000), partial: true, wantPartial: true},
		{name: "fetch failure", failure: errors.New("backend unavailable")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var baseline []string
			var calls []int
			for _, enabled := range []bool{false, true} {
				SharedTopologyReuseMatrix = enabled
				ctx := initTimeGraphQueryTestEnvironment()
				model := sharedTopologyQueryModel(nil)
				provider := sharedTopologyQueryProvider().(contractSchemaProvider)
				provider.schemas[0].IsDirectional = false
				model.schemaProvider = provider
				model.timeGraphQueryReference = func(ctx context.Context, query *structured.QueryTs) (metadata.QueryReference, error) {
					ref, err := timeGraphTestQueryReference(ctx, query)
					metadata.GetQueryParams(ctx).SetStorageType(metadata.VictoriaMetricsStorageType)
					return ref, err
				}
				count := 0
				model.timeGraphVMQueryWithPartial = func(_ context.Context, query *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, bool, error) {
					count++
					if query.QueryList[0].FieldName == "source_middle_flow" {
						return tt.matrix, tt.partial, tt.failure
					}
					return nil, false, nil
				}
				request := cmdb.SharedTopologyQuery{
					SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"},
					StartTime: 1700000000, EndTime: 1700000060, Step: "60s", MaxHops: 2,
				}
				result, err := model.QuerySharedTopology(ctx, request)
				calls = append(calls, count)
				if tt.failure != nil {
					require.ErrorContains(t, err, tt.failure.Error())
					require.Empty(t, result.Snapshots)
					continue
				}
				require.NoError(t, err)
				require.Len(t, result.Snapshots, 2)
				for _, snapshot := range result.Snapshots {
					require.Equal(t, tt.wantPartial, snapshot.Partial)
					if tt.wantPartial {
						require.Equal(t, "backend_partial", snapshot.PartialReason)
					} else {
						require.Empty(t, snapshot.PartialReason)
					}
				}
				canonical := canonicalTopologyForTest(t, result.Snapshots)
				if baseline == nil {
					baseline = canonical
				} else {
					require.Equal(t, baseline, canonical)
				}
				request.ResponseFormat = cmdb.CompactTopologyFormat
				compact, err := model.QuerySharedTopology(ctx, request)
				require.NoError(t, err)
				require.Equal(t, result.Snapshots, decodeCompactForTest(t, compact.Compact))
			}
			require.Len(t, calls, 2)
			t.Logf("physical fetches with reuse disabled/enabled: %d/%d", calls[0], calls[1])
			if tt.partial {
				require.Less(t, calls[1], calls[0], "reuse must reduce physical fetches without hiding partial")
			} else {
				require.Equal(t, calls[0], calls[1], "complete empty and failed fetches stop before repeated relation reads")
			}
		})
	}
}
