// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package v1beta3

import (
	"context"
	"errors"
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

func TestTopologyReadyGroupFailureReleasesWorkersAndReservations(t *testing.T) {
	for _, unlimited := range []bool{false, true} {
		for _, outcome := range []string{"backend", "apply", "cancel"} {
			t.Run(strconv.FormatBool(unlimited)+"/"+outcome, func(t *testing.T) {
				topologyAdmissionTestEnvironment(t, 1)
				MaxSharedTopologyReservedBytes = 1 << 30
				topologyMemoryHeadroom = func() (int64, bool) { return 4 << 30, true }
				yoloMode = unlimited
				parent, cancel := context.WithTimeout(metadata.WithExactTimeGrid(initTimeGraphQueryTestEnvironment()), time.Second)
				defer cancel()
				ctx, release, err := AcquireSharedTopology(parent)
				require.NoError(t, err)
				defer release()
				provider := selectorProvider()
				provider.schemas = provider.schemas[:1]
				model := &Model{schemaProvider: provider}
				grid, err := NewTopologyGrid([]int64{1700000000000})
				require.NoError(t, err)
				graph := newSharedTimeGraph(model.timeGraphConfig("space"), grid)
				defer graph.Clean(ctx)
				require.NoError(t, graph.enableResidency(ctx))
				loader := &timeGraphMatrixLoader{model: model, graph: graph, topology: true, start: time.Unix(1700000000, 0), end: time.Unix(1700000000, 0), step: time.Minute}
				started := make(chan struct{})
				var active atomic.Int32
				failure := errors.New("later group backend unavailable")
				loader.override = func(ctx context.Context, query *structured.QueryTs) (pl.Matrix, error) {
					active.Add(1)
					defer active.Add(-1)
					if query.SpaceUid == "0" {
						close(started)
						<-ctx.Done()
						return nil, ctx.Err()
					}
					select {
					case <-started:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					switch outcome {
					case "cancel":
						cancel()
						return nil, ctx.Err()
					case "apply":
						return contractMatrix(map[string]string{"a_id": "root"}, grid.timestamps...), nil
					default:
						return nil, failure
					}
				}
				relations := model.sharedTopologyRelations("space", nil, nil, DirectionBoth)
				groups := make([]topologyFetchGroup, 2)
				for i := range groups {
					groups[i] = topologyFetchGroup{ctx: ctx, query: &structured.QueryTs{SpaceUid: strconv.Itoa(i), Start: "1700000000", End: "1700000000", Step: "1m0s"}, relations: relations}
				}
				queries := 0
				err = loader.fetchTopologyGroups(groups, "space", &queries, nil, true)
				if outcome == "backend" {
					require.ErrorIs(t, err, failure)
				} else if outcome == "cancel" {
					require.ErrorIs(t, err, context.Canceled)
				} else {
					require.ErrorContains(t, err, "b_id")
				}
				require.Zero(t, active.Load())
				require.Zero(t, len(topologyBackendSlots))
				require.Zero(t, matrixResidencyState(graph).ReceivingBytes)
				require.Zero(t, matrixResidencyState(graph).ResidentBytes)
				graph.Clean(ctx)
				release()
				require.Equal(t, map[string]int64{"active": 0, "waiters": 0, "reserved": 0, "materialized": 0}, topologyAdmissionTestState())
			})
		}
	}
}

func TestTopologyAttributeSensitiveGroupsKeepOrderedApplication(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	provider := selectorProvider()
	provider.schemas = provider.schemas[:1]
	provider.fields["a"] = []string{"a_id", "b_id"}
	model := &Model{schemaProvider: provider}
	grid, err := NewTopologyGrid([]int64{1700000000000})
	require.NoError(t, err)
	var baseline []string
	for _, parallel := range []bool{false, true} {
		ctx := metadata.WithExactTimeGrid(initTimeGraphQueryTestEnvironment())
		graph := newSharedTimeGraph(model.timeGraphConfig("space"), grid)
		loader := &timeGraphMatrixLoader{model: model, graph: graph, topology: true, start: time.Unix(1700000000, 0), end: time.Unix(1700000000, 0), step: time.Minute}
		loader.override = func(_ context.Context, query *structured.QueryTs) (pl.Matrix, error) {
			return contractMatrix(map[string]string{"a_id": "root", "b_id": query.SpaceUid}, grid.timestamps...), nil
		}
		relations := model.sharedTopologyRelations("space", nil, nil, DirectionBoth)
		groups := make([]topologyFetchGroup, 2)
		for i := range groups {
			groups[i] = topologyFetchGroup{ctx: ctx, query: &structured.QueryTs{SpaceUid: strconv.Itoa(1 - i), Start: "1700000000", End: "1700000000", Step: "1m0s"}, relations: relations}
		}
		require.False(t, loader.canApplyTopologyGroupsAsReady(groups, nil))
		queries := 0
		require.NoError(t, loader.fetchTopologyGroups(groups, "space", &queries, nil, parallel))
		snapshots, err := graph.findSharedTopologyDTO(ctx, grid, SharedTopologyQuery{SourceType: "a", SourceMatcher: cmdb.Matcher{"a_id": "root"}, MaxHops: 2})
		require.NoError(t, err)
		canonical := canonicalTopologyForTest(t, snapshots)
		if !parallel {
			baseline = canonical
		} else {
			require.Equal(t, baseline, canonical, "a projected non-primary attribute retains the original overwrite order")
		}
		graph.Clean(ctx)
	}
}

func TestTopologySafeGroupsDrainShardsWithoutWaitingForFirstGroup(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	provider := selectorProvider()
	provider.schemas = provider.schemas[:1]
	model := &Model{schemaProvider: provider}
	grid, err := NewTopologyGrid([]int64{1700000000000, 1700000060000})
	require.NoError(t, err)
	relations := model.sharedTopologyRelations("space", nil, nil, DirectionBoth)
	var baseline []string
	for _, parallel := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(metadata.WithExactTimeGrid(initTimeGraphQueryTestEnvironment()), time.Second)
		graph := newSharedTimeGraph(model.timeGraphConfig("space"), grid)
		secondFinished := make(chan struct{})
		loader := &timeGraphMatrixLoader{model: model, graph: graph, topology: true, start: time.Unix(1700000000, 0), end: time.Unix(1700000060, 0), step: time.Minute}
		loader.override = func(ctx context.Context, query *structured.QueryTs) (pl.Matrix, error) {
			if query.SpaceUid == "0" && parallel {
				select {
				case <-secondFinished:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			if query.SpaceUid == "1" && query.Start != query.End {
				return nil, metadata.ErrBackendResponseTooLarge
			}
			if query.SpaceUid == "1" && query.Start == "1700000060" {
				close(secondFinished)
			}
			start, end, step, _, ok := timeGraphQueryRange(query)
			if !ok {
				return nil, context.Canceled
			}
			timestamps := []int64{}
			for stamp := start; stamp <= end; stamp += step {
				timestamps = append(timestamps, stamp)
			}
			return contractMatrix(map[string]string{"a_id": "root", "b_id": query.SpaceUid}, timestamps...), nil
		}
		groups := make([]topologyFetchGroup, 2)
		for i := range groups {
			groups[i] = topologyFetchGroup{ctx: ctx, query: &structured.QueryTs{SpaceUid: strconv.Itoa(i), Start: "1700000000", End: "1700000060", Step: "1m0s"}, relations: relations}
		}
		queries := 0
		err = loader.fetchTopologyGroups(groups, "space", &queries, nil, parallel)
		cancel()
		if err != nil {
			graph.Clean(context.Background())
			t.Fatalf("parallel=%v: a ready later group's next shard must not wait for the first group: %v", parallel, err)
		}
		require.Equal(t, 2, queries)
		require.Equal(t, 4, loader.physicalQueryCount)
		snapshots, err := graph.findSharedTopologyDTO(context.Background(), grid, SharedTopologyQuery{SourceType: "a", SourceMatcher: cmdb.Matcher{"a_id": "root"}, MaxHops: 2})
		require.NoError(t, err)
		require.Len(t, snapshots, 2)
		canonical := canonicalTopologyForTest(t, snapshots)
		if !parallel {
			baseline = canonical
		} else {
			require.Equal(t, baseline, canonical, "complete attributes, relations, quality and both frames remain identical across request-local ID allocation orders")
		}
		graph.Clean(context.Background())
	}
}

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
	graph := newSharedTimeGraph(model.timeGraphConfig("space"), grid)
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
		return contractMatrix(map[string]string{"a_id": "root", "b_id": query.SpaceUid}, grid.timestamps...), nil
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
