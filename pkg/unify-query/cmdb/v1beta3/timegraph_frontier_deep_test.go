package v1beta3

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"
)

func TestTopologyDeepFrontierFull60WindowMatchesWideAndAvoidsUnrelatedSeries(t *testing.T) {
	oldPlan, oldYolo := SharedTopologyPlanCandidates, yoloMode
	yoloMode = true
	t.Cleanup(func() { SharedTopologyPlanCandidates = oldPlan; yoloMode = oldYolo })
	const stamp int64 = 1700000000000
	times := make([]int64, 60)
	for i := range times {
		times[i] = stamp + int64(i)*60000
	}
	data := map[string]pl.Matrix{}
	add := func(metric string, labels map[string]string, ts ...int64) {
		data[metric] = append(data[metric], contractMatrix(labels, ts...)...)
	}
	add("cp", map[string]string{"cluster": "alpha", "namespace": "one", "container_id": "root", "pod_id": "p0"}, times...)
	add("np", map[string]string{"cluster": "alpha", "namespace": "one", "node_id": "n0", "pod_id": "p0"}, times...)
	add("ns", map[string]string{"cluster": "alpha", "node_id": "n0", "system_id": "bridge"}, times...)
	add("ns", map[string]string{"cluster": "beta", "node_id": "n1", "system_id": "bridge"}, times[:30]...)
	add("hs", map[string]string{"host_id": "h0", "system_id": "bridge"}, times...)
	add("np", map[string]string{"cluster": "beta", "namespace": "two", "node_id": "n1", "pod_id": "p1"}, times...)
	add("cp", map[string]string{"cluster": "beta", "namespace": "two", "container_id": "c1", "pod_id": "p1"}, times...)
	// A union frontier may fetch this neighbor, but a cross-frame path must not
	// make it reachable in a snapshot without the simultaneous System bridge.
	add("np", map[string]string{"cluster": "beta", "namespace": "two", "node_id": "n1", "pod_id": "p2"}, times[30:]...)
	add("cp", map[string]string{"cluster": "beta", "namespace": "two", "container_id": "c2", "pod_id": "p2"}, times...)
	// The non-tree edge must be retained as an induced edge.
	add("np", map[string]string{"cluster": "alpha", "namespace": "one", "node_id": "n1", "pod_id": "p0"}, times[:10]...)
	for i := 0; i < 2000; i++ {
		id := fmt.Sprint(i)
		add("cp", map[string]string{"cluster": "noise", "namespace": "noise", "container_id": "c" + id, "pod_id": "p" + id}, times...)
		add("np", map[string]string{"cluster": "noise", "namespace": "noise", "node_id": "n" + id, "pod_id": "p" + id}, times...)
		add("ns", map[string]string{"cluster": "noise", "node_id": "n" + id, "system_id": "s" + id}, times...)
		add("hs", map[string]string{"host_id": "h" + id, "system_id": "s" + id}, times...)
	}
	request := cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "container", SourceInfo: cmdb.Matcher{"cluster": "alpha", "namespace": "one", "container_id": "root"}, StartTime: stamp / 1000, EndTime: stamp/1000 + 59*60, Step: "60s", MaxHops: 10}
	var baseline []string
	counts := []int{}
	for _, planned := range []bool{false, true} {
		SharedTopologyPlanCandidates = planned
		fetched := 0
		model := &Model{schemaProvider: selectorScopeProvider(), timeGraphQueryReference: selectorQueryReference}
		model.timeGraphVMQuery = func(_ context.Context, ts *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
			value := filteredTimeGraphMatrix(t, data[ts.QueryList[0].FieldName], ts.QueryList[0].Conditions)
			fetched += len(value)
			return value, nil
		}
		got, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
		require.NoError(t, err)
		canonical := canonicalTopologyForTest(t, got.Snapshots)
		if !planned {
			baseline = canonical
		} else {
			require.Equal(t, baseline, canonical)
		}
		counts = append(counts, fetched)
		request.ResponseFormat = cmdb.CompactTopologyFormat
		compact, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
		require.NoError(t, err)
		require.Equal(t, canonical, canonicalTopologyForTest(t, decodeCompactForTest(t, compact.Compact)))
		request.ResponseFormat = ""
	}
	t.Logf("DEEP_FRONTIER_COUNTS wide=%d selected=%d", counts[0], counts[1])
	require.Less(t, counts[1]*10, counts[0], "deep identity plan must avoid irrelevant wide relations while preserving all60 answers")
}

func TestTopologyTimeShardsReuseSuccessfulGridWidth(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	ctx := metadata.WithExactTimeGrid(initTimeGraphQueryTestEnvironment())
	start := time.Unix(1700000000, 0)
	graph := NewTimeGraphWithConfig((&Model{schemaProvider: selectorProvider()}).timeGraphConfig("space"))
	model := &Model{}
	attempts, failed, points := 0, 0, 0
	loader := &timeGraphMatrixLoader{model: model, graph: graph, start: start, end: start.Add(59 * time.Minute), step: time.Minute, topology: true}
	loader.override = func(_ context.Context, ts *structured.QueryTs) (pl.Matrix, error) {
		attempts++
		a, e := parseTimestamp(ts.Start)
		require.NoError(t, e)
		z, e := parseTimestamp(ts.End)
		require.NoError(t, e)
		n := int((z-a)/60000) + 1
		if n > 2 {
			failed++
			return nil, metadata.ErrBackendResponseTooLarge
		}
		times := make([]int64, n)
		for i := range times {
			times[i] = a + int64(i)*60000
		}
		return contractMatrix(map[string]string{"a_id": "root", "b_id": "b"}, times...), nil
	}
	query := &structured.QueryTs{Start: "1700000000", End: "1700003540", Step: "1m0s"}
	seen := map[int64]int{}
	err := loader.queryAndApply(ctx, query, func(_ context.Context, m pl.Matrix) error {
		for _, s := range m {
			for _, p := range s.Points {
				seen[p.T]++
				points++
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 60, points)
	require.Len(t, seen, 60)
	for i := 0; i < 60; i++ {
		require.Equal(t, 1, seen[start.UnixMilli()+int64(i)*60000])
	}
	t.Logf("TIME_SHARD_COUNTS attempts=%d oversized=%d points=%d", attempts, failed, points)
	require.LessOrEqual(t, failed, 6, "successful leaves should stop oversized probes in the later siblings")
}
