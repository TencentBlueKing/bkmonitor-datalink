// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	uqjson "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"
)

func decodeCompactForTest(t testing.TB, compact *cmdb.CompactTopology) []cmdb.SharedTopologySnapshot {
	t.Helper()
	active := func(mask []string, frame int) bool {
		if frame/64 >= len(mask) {
			return false
		}
		word, err := strconv.ParseUint(mask[frame/64], 16, 64)
		require.NoError(t, err)
		return word&(uint64(1)<<uint(frame%64)) != 0
	}
	id := func(value string) uint64 {
		result, err := strconv.ParseUint(value, 10, 64)
		require.NoError(t, err)
		return result
	}
	result := make([]cmdb.SharedTopologySnapshot, len(compact.Timestamps))
	for i, ts := range compact.Timestamps {
		result[i] = cmdb.SharedTopologySnapshot{Timestamp: ts, Nodes: []cmdb.SharedTopologyNode{}, Edges: []cmdb.SharedTopologyEdge{}}
		for _, node := range compact.Nodes {
			if active(node.Mask, i) {
				result[i].Nodes = append(result[i].Nodes, cmdb.SharedTopologyNode{ID: id(node.ID), ResourceType: node.ResourceType, Dimensions: cloneMatcher(node.Dimensions)})
			}
		}
		for _, edge := range compact.Edges {
			if active(edge.Mask, i) {
				result[i].Edges = append(result[i].Edges, cmdb.SharedTopologyEdge{Source: id(edge.Source), Target: id(edge.Target), RelationType: edge.RelationType, MetricName: edge.MetricName, Category: edge.Category, Direction: edge.Direction})
			}
		}
	}
	for _, partial := range compact.Partial {
		result[partial.Index].Partial = true
		result[partial.Index].PartialReason = partial.Reason
	}
	return result
}

func TestCompactOwnershipFallbackBudgetAndClient(t *testing.T) {
	oldYolo := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = oldYolo })
	ctx := context.Background()
	grid, err := NewTopologyGrid(sharedGraphTestTimes(129))
	require.NoError(t, err)
	graph, err := newSharedTimeGraph(sharedGraphTestConfig(), grid)
	require.NoError(t, err)
	require.NoError(t, graph.AddTimeRelationWithRelation(ctx, sharedGraphTestRelation("calls"), cmdb.Matcher{"from_id": "a", "to_id": "b"}, grid.Timestamps...))
	require.NoError(t, graph.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": "a", "version": "old <&>", "name": "\u2028"}, grid.Timestamps[:64]...))
	require.NoError(t, graph.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": "a", "version": "new"}, grid.Timestamps[64:]...))
	// Exercise stable identity fallback when no version covers a reachable node.
	for id := range graph.shared.nodeInfos {
		_, info := graph.nodeBuilder.Info(id)
		if info["id"] == "b" {
			delete(graph.shared.nodeInfos, id)
		}
	}
	query := SharedTopologyQuery{SourceType: "entity", SourceMatcher: cmdb.Matcher{"id": "a"}, MaxHops: 1, PartialTimestamps: map[int64]string{grid.Timestamps[64]: "backend_partial", grid.Timestamps[128]: ""}}
	want, err := graph.FindSharedTopology(ctx, grid, query)
	require.NoError(t, err)
	compact, err := graph.FindCompactTopology(ctx, grid, query, nil)
	require.NoError(t, err)
	graph.Clean(ctx)
	require.Equal(t, convertSharedTopologySnapshots(want), decodeCompactForTest(t, compact))
	require.Equal(t, 258, compact.NodeOccurrences)
	require.Equal(t, 129, compact.EdgeOccurrences)
	if node, err := exec.LookPath("node"); err == nil {
		// Expected IDs are strings before JSON encoding, avoiding a lossy JS parse.
		expected := make([]map[string]any, len(want))
		for i, snapshot := range convertSharedTopologySnapshots(want) {
			nodes := make([]map[string]any, 0, len(snapshot.Nodes))
			edges := make([]map[string]any, 0, len(snapshot.Edges))
			for _, n := range snapshot.Nodes {
				nodes = append(nodes, map[string]any{"id": strconv.FormatUint(n.ID, 10), "resource_type": n.ResourceType, "dimensions": n.Dimensions})
			}
			for _, e := range snapshot.Edges {
				edges = append(edges, map[string]any{"source": strconv.FormatUint(e.Source, 10), "target": strconv.FormatUint(e.Target, 10), "relation_type": e.RelationType, "metric_name": e.MetricName, "category": e.Category, "direction": e.Direction})
			}
			expected[i] = map[string]any{"timestamp": snapshot.Timestamp, "nodes": nodes, "edges": edges, "partial": snapshot.Partial}
			if snapshot.PartialReason != "" {
				expected[i]["partial_reason"] = snapshot.PartialReason
			}
		}
		encoded, err := json.Marshal([]any{map[string]any{"compact": compact, "snapshots": expected}})
		require.NoError(t, err)
		path := filepath.Join(t.TempDir(), "compact.json")
		require.NoError(t, os.WriteFile(path, encoded, 0600))
		cmd := exec.Command(node, "--test", "../../docs/examples/topology-compact.test.mjs")
		cmd.Env = append(os.Environ(), "TG_COMPACT_FIXTURE="+path)
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))
	} else {
		t.Log("Node unavailable: standalone JS tests not executed")
	}
}

func TestCompactModelTargetFilterAndBudgets(t *testing.T) {
	ctx := initTimeGraphQueryTestEnvironment()
	model := sharedTopologyQueryModel(map[string]pl.Matrix{
		"source_info_relation": contractMatrix(map[string]string{"source_id": "a"}, 1700000000000),
		"source_middle_flow":   contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, 1700000000000),
		"middle_target_flow":   contractMatrix(map[string]string{"middle_id": "b", "target_id": "c"}, 1700000000000),
	})
	request := cmdb.SharedTopologyQuery{SpaceUID: "space", Timestamp: 1700000000, SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, TargetTypes: []cmdb.Resource{"target"}, MaxHops: 2}
	want, err := model.QuerySharedTopology(ctx, request)
	require.NoError(t, err)
	request.ResponseFormat = cmdb.CompactTopologyFormat
	got, err := model.QuerySharedTopology(ctx, request)
	require.NoError(t, err)
	require.Equal(t, want.Snapshots, decodeCompactForTest(t, got.Compact))
	old := MaxSharedTopologyOutputElements
	MaxSharedTopologyOutputElements = 1
	t.Cleanup(func() { MaxSharedTopologyOutputElements = old })
	_, err = model.QuerySharedTopology(ctx, request)
	require.ErrorContains(t, err, "max_topology_output_elements")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = model.QuerySharedTopology(canceled, request)
	require.ErrorIs(t, err, context.Canceled)
}

func BenchmarkTopologyOutputFormats(b *testing.B) {
	oldYolo := yoloMode
	yoloMode = true
	defer func() { yoloMode = oldYolo }()
	ctx := context.Background()
	grid, err := NewTopologyGrid(sharedGraphTestTimes(60))
	require.NoError(b, err)
	graph, err := newSharedTimeGraph(sharedGraphTestConfig(), grid)
	require.NoError(b, err)
	defer graph.Clean(ctx)
	for i := 0; i < 1000; i++ {
		require.NoError(b, graph.AddTimeRelationWithRelation(ctx, sharedGraphTestRelation("calls"), cmdb.Matcher{"from_id": "root", "to_id": fmt.Sprint(i)}, grid.Timestamps...))
	}
	query := SharedTopologyQuery{SourceType: "entity", SourceMatcher: cmdb.Matcher{"id": "root"}, MaxHops: 1}
	for _, format := range []string{"snapshots", "compact"} {
		b.Run(format, func(b *testing.B) {
			b.ReportAllocs()
			run := func() {
				var output any
				if format == "snapshots" {
					snapshots, e := graph.FindSharedTopology(ctx, grid, query)
					require.NoError(b, e)
					output = convertSharedTopologySnapshots(snapshots)
				} else {
					output, err = graph.FindCompactTopology(ctx, grid, query, nil)
					require.NoError(b, err)
				}
				encoded, e := uqjson.Marshal(output)
				require.NoError(b, e)
				b.ReportMetric(float64(len(encoded)), "wire-bytes")
			}
			if os.Getenv("TG_BENCH_WARM") == "true" {
				run()
				b.ResetTimer()
			}
			for i := 0; i < b.N; i++ {
				run()
			}
		})
	}
}

func TestCompactLargeFixtureDifferential(t *testing.T) {
	value := os.Getenv("TG_COMPACT_VALIDATE_EDGES")
	if value == "" {
		t.Skip("set TG_COMPACT_VALIDATE_EDGES for a separate large-fixture correctness run")
	}
	n, err := strconv.Atoi(value)
	require.NoError(t, err)
	require.Positive(t, n)
	oldYolo := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = oldYolo })
	ctx := context.Background()
	grid, err := NewTopologyGrid(sharedGraphTestTimes(60))
	require.NoError(t, err)
	graph, err := newSharedTimeGraph(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "source", Index: cmdb.Index{"source_id"}}, {Name: "middle", Index: cmdb.Index{"middle_id"}}}}, grid)
	require.NoError(t, err)
	defer graph.Clean(ctx)
	relation := cmdb.Relation{V: []cmdb.Resource{"source", "middle"}, RelationType: "source_to_middle", MetricName: "source_middle_flow", Category: "static", Direction: "outbound"}
	for i := 0; i < n; i++ {
		require.NoError(t, graph.AddTimeRelationWithRelation(ctx, relation, cmdb.Matcher{"source_id": "a", "middle_id": strconv.Itoa(i)}, grid.Timestamps...))
	}
	query := SharedTopologyQuery{SourceType: "source", SourceMatcher: cmdb.Matcher{"source_id": "a"}, MaxHops: 1}
	want, err := graph.FindSharedTopology(ctx, grid, query)
	require.NoError(t, err)
	compact, err := graph.FindCompactTopology(ctx, grid, query, nil)
	require.NoError(t, err)
	got := decodeCompactForTest(t, compact)
	require.Equal(t, 60, len(got))
	for index, snapshot := range convertSharedTopologySnapshots(want) {
		require.Len(t, snapshot.Nodes, n+1)
		require.Len(t, snapshot.Edges, n)
		require.True(t, reflect.DeepEqual(snapshot, got[index]), "full frame mismatch at %d", index)
	}
}
