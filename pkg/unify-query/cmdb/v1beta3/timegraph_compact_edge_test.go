package v1beta3

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOwnedCompactEdgeStoreMatchesLegacyFullIdentityAndClears(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	provider := selectorProvider()
	model := &Model{schemaProvider: provider}
	relations := model.sharedTopologyRelations("space", nil, nil, DirectionBoth)
	for _, points := range []int{3, 65, 130} {
		times := make([]int64, points)
		for i := range times {
			times[i] = 1700000000000 + int64(i)*60000
		}
		grid, err := NewTopologyGrid(times)
		require.NoError(t, err)
		var baseline []byte
		var baselineCount int
		for _, compact := range []bool{false, true} {
			graph := newSharedTimeGraph(model.timeGraphConfig("space"), grid)
			if compact {
				graph.shared.relationIDs = make(map[timeGraphEdgeRelation]uint64)
				if points <= 64 {
					graph.shared.wordEdges = make(map[sharedCompactEdgeKey]uint64)
					graph.shared.wordPairs = make(map[timeGraphEdgeKey]uint64)
				} else {
					graph.shared.compactEdges = make(map[sharedCompactEdgeKey]timeBitmap)
				}
			}
			for i, r := range relations[:2] {
				info := map[string]string{"a_id": "root", "b_id": "one"}
				r.MetricName = "metric-a"
				require.NoError(t, graph.AddTimeRelationWithRelation(context.Background(), r, info, times...))
				r.MetricName = "metric-b"
				require.NoError(t, graph.AddTimeRelationWithRelation(context.Background(), r, info, times[i:]...))
			}
			got, err := graph.findSharedTopologyDTO(context.Background(), grid, SharedTopologyQuery{SourceType: "a", MaxHops: 10})
			require.NoError(t, err)
			wire, err := json.Marshal(got)
			require.NoError(t, err)
			if !compact {
				baseline = wire
				baselineCount = graph.edgeCount
			} else {
				require.Equal(t, string(baseline), string(wire))
				require.Equal(t, 4, graph.shared.edgeCount())
				require.Equal(t, baselineCount, graph.edgeCount, "logical occurrence metrics retain duplicate-pair semantics")
			}
			graph.Clean(context.Background())
			require.Zero(t, graph.shared.edgeCount())
			require.Empty(t, graph.shared.wordPairs)
			require.Empty(t, graph.shared.relationIDs)
			require.Empty(t, graph.shared.relations)
			require.Equal(t, [2]sharedRelationSlot{}, graph.shared.relationCache)
		}
	}
}
