// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package v1beta3

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestTopologyBackendFetchesShareFourProcessSlots(t *testing.T) {
	var active, peak atomic.Int32
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	done := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			loader := &timeGraphMatrixLoader{topology: true}
			_, _, err := loader.fetchResident(context.Background(), func() (pl.Matrix, bool, error) {
				current := active.Add(1)
				for previous := peak.Load(); current > previous && !peak.CompareAndSwap(previous, current); previous = peak.Load() {
				}
				started <- struct{}{}
				<-release
				active.Add(-1)
				return nil, false, nil
			})
			done <- err
		}()
	}
	for i := 0; i < 4; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			close(release)
			t.Fatal("four backend calls did not start")
		}
	}
	close(release)
	wg.Wait()
	for i := 0; i < 8; i++ {
		require.NoError(t, <-done)
	}
	require.Equal(t, int32(4), peak.Load())
	require.Zero(t, active.Load())
}

func TestTopologyParallelFetchAppliesGroupsInOriginalOrder(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	ctx := metadata.WithExactTimeGrid(initTimeGraphQueryTestEnvironment())
	provider := selectorProvider()
	provider.schemas = provider.schemas[:1]
	model := &Model{schemaProvider: provider}
	grid, err := NewTopologyGrid([]int64{1700000000000})
	require.NoError(t, err)
	graph, err := newSharedTimeGraph(model.timeGraphConfig("space"), grid)
	require.NoError(t, err)
	defer graph.Clean(ctx)
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	loader := &timeGraphMatrixLoader{model: model, graph: graph, topology: true, start: time.Unix(1700000000, 0), end: time.Unix(1700000000, 0), step: time.Minute}
	loader.override = func(ctx context.Context, query *structured.QueryTs) (pl.Matrix, error) {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return contractMatrix(map[string]string{"a_id": "root", "b_id": query.SpaceUid}, grid.Timestamps...), nil
	}
	relations := model.sharedTopologyRelations("space", nil, nil, DirectionBoth)[:2]
	groups := make([]topologyFetchGroup, 4)
	for i := range groups {
		groups[i] = topologyFetchGroup{ctx: ctx, query: &structured.QueryTs{SpaceUid: strconv.Itoa(i), Start: "1700000000", End: "1700000000", Step: "1m0s"}, relations: relations}
	}
	queries := 0
	var observed []string
	result := make(chan error, 1)
	go func() {
		result <- loader.fetchTopologyGroups(groups, "space", &queries, func(_ cmdb.Relation, matrix pl.Matrix) {
			observed = append(observed, matrix[0].Metric.Get("b_id"))
		}, true)
	}()
	for i := 0; i < 4; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			close(release)
			t.Fatal("independent groups did not start together")
		}
	}
	close(release)
	require.NoError(t, <-result)
	require.Equal(t, []string{"0", "0", "1", "1", "2", "2", "3", "3"}, observed)
	require.Equal(t, 4, queries)
	require.Equal(t, 4, loader.physicalQueryCount)
}

func TestFullRootStartsFourIndependentRelationFetches(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	data := map[string]pl.Matrix{
		"cp": contractMatrix(map[string]string{"cluster": "alpha", "namespace": "one", "container_id": "c0", "pod_id": "p0"}, 1700000000000),
		"np": contractMatrix(map[string]string{"cluster": "alpha", "namespace": "one", "node_id": "n0", "pod_id": "p0"}, 1700000000000),
		"ns": contractMatrix(map[string]string{"cluster": "alpha", "node_id": "n0", "system_id": "s0"}, 1700000000000),
		"hs": contractMatrix(map[string]string{"host_id": "h0", "system_id": "s0"}, 1700000000000),
	}
	started := make(chan string, 8)
	release := make(chan struct{})
	model := &Model{schemaProvider: selectorScopeProvider(), timeGraphQueryReference: selectorQueryReference}
	model.timeGraphVMQuery = func(ctx context.Context, query *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
		started <- query.QueryList[0].FieldName
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return data[query.QueryList[0].FieldName], nil
	}
	ctx, cancel := context.WithTimeout(initTimeGraphQueryTestEnvironment(), 3*time.Second)
	defer cancel()
	result := make(chan struct {
		value cmdb.SharedTopologyResult
		err   error
	}, 1)
	go func() {
		value, err := model.QuerySharedTopology(ctx, cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "container", Timestamp: 1700000000, MaxHops: 10})
		result <- struct {
			value cmdb.SharedTopologyResult
			err   error
		}{value, err}
	}()
	var names []string
	for i := 0; i < 4; i++ {
		select {
		case name := <-started:
			names = append(names, name)
		case outcome := <-result:
			close(release)
			t.Fatalf("full-root query stopped after %v: %v", names, outcome.err)
		case <-ctx.Done():
			close(release)
			t.Fatalf("full-root query did not start four independent backend calls: %v", names)
		}
	}
	close(release)
	outcome := <-result
	require.NoError(t, outcome.err)
	require.Len(t, outcome.value.Snapshots, 1)
	require.False(t, outcome.value.Snapshots[0].Partial)
	require.Len(t, outcome.value.Snapshots[0].Nodes, 5)
	require.Len(t, outcome.value.Snapshots[0].Edges, 8)
}
