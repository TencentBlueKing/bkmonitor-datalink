package v1beta3

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"
)

func TestSharedUniformGridBitsMatchTimestampMapIncludingRestAndIrregular(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	uniform := make([]int64, 130)
	for i := range uniform {
		uniform[i] = 1700000000000 + int64(i)*60000
	}
	for _, timestamps := range [][]int64{uniform, {10, 20, 35, 50}, {10}, {math.MinInt64 + 1, -1, math.MaxInt64 - 1}} {
		grid, err := NewTopologyGrid(timestamps)
		require.NoError(t, err)
		graph, err := newSharedTimeGraph(nil, grid)
		require.NoError(t, err)
		defer graph.Clean(context.Background())
		selected := []int64{timestamps[len(timestamps)-1], timestamps[0], timestamps[0]}
		got, err := graph.shared.timeBits(selected)
		require.NoError(t, err)
		want := timeBitmap{}
		for _, ts := range selected {
			want.set(graph.shared.timestampIndex[ts])
		}
		require.Equal(t, want, got)
		_, err = graph.shared.timeBits([]int64{timestamps[0] + 1})
		require.ErrorContains(t, err, "does not match topology time grid")
	}
}

func TestReversePairKeepsLogicalMatrixChargeAfterSingleGridValidation(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	ctx := metadata.WithExactTimeGrid(initTimeGraphQueryTestEnvironment())
	provider := selectorProvider()
	provider.schemas = provider.schemas[:1]
	model := &Model{schemaProvider: provider}
	grid, err := NewTopologyGrid([]int64{1700000000000, 1700000060000, 1700000120000})
	require.NoError(t, err)
	graph, err := newSharedTimeGraph(model.timeGraphConfig("space"), grid)
	require.NoError(t, err)
	defer graph.Clean(ctx)
	loader := &timeGraphMatrixLoader{model: model, graph: graph, topology: true, start: time.Unix(1700000000, 0), end: time.Unix(1700000120, 0), step: time.Minute, override: func(context.Context, *structured.QueryTs) (pl.Matrix, error) {
		return contractMatrix(map[string]string{"a_id": "root", "b_id": "one"}, grid.Timestamps...), nil
	}}
	relations := model.sharedTopologyRelations("space", nil, nil, DirectionBoth)[:2]
	require.True(t, graph.topologyReversePair(relations))
	queries := 0
	require.NoError(t, loader.fetchTopologyGroup(topologyFetchGroup{ctx: ctx, query: &structured.QueryTs{Start: "1700000000", End: "1700000120", Step: "1m0s"}, relations: relations}, "space", &queries, nil))
	require.Equal(t, 1, queries)
	require.Equal(t, 1, loader.physicalQueryCount)
	require.Equal(t, 1, loader.reusedQueryCount)
	require.Equal(t, 6, loader.pointCount)
}
