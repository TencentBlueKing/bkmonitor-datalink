package v1beta3

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"
)

func TestTopologyReversePairMatchesOrderedFullAttributeAndWireApplication(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	times := make([]int64, 65)
	for i := range times {
		times[i] = 1700000000000 + int64(i)*60000
	}
	grid, err := NewTopologyGrid(times)
	require.NoError(t, err)
	for _, dynamic := range []bool{false, true} {
		t.Run(map[bool]string{false: "static-shared-attributes", true: "dynamic-composite-self"}[dynamic], func(t *testing.T) {
			provider := selectorProvider()
			provider.schemas = provider.schemas[:1]
			provider.fields["a"] = []string{"a_id", "b_id", "version"}
			provider.fields["b"] = []string{"b_id", "a_id", "version"}
			data := pl.Matrix{}
			data = append(data, contractMatrix(map[string]string{"a_id": "root", "b_id": "one", "version": "alpha"}, times[:40]...)...)
			data = append(data, contractMatrix(map[string]string{"a_id": "root", "b_id": "two", "version": "zeta"}, times[20:]...)...)
			data = append(data, contractMatrix(map[string]string{"a_id": "other", "b_id": "one", "version": "last"}, times[40:]...)...)
			source, matcher := cmdb.Resource("a"), cmdb.Matcher{"a_id": "root"}
			if dynamic {
				provider = frontierCompositeProvider()
				source = "service"
				matcher = cmdb.Matcher{"tenant": "x|y", "id": "root"}
				data = nil
				data = append(data, contractMatrix(map[string]string{"from_tenant": "x|y", "from_id": "root", "from_version": "alpha", "to_tenant": "z", "to_id": "one", "to_version": "beta"}, times[:40]...)...)
				data = append(data, contractMatrix(map[string]string{"from_tenant": "x|y", "from_id": "root", "from_version": "zeta", "to_tenant": "z", "to_id": "two", "to_version": "gamma"}, times[20:]...)...)
				data = append(data, contractMatrix(map[string]string{"from_tenant": "z", "from_id": "one", "from_version": "last", "to_tenant": "x|y", "to_id": "root", "to_version": "theta"}, times[40:]...)...)
			}
			model := &Model{schemaProvider: provider}
			relations := model.sharedTopologyRelations("space", nil, nil, DirectionBoth)
			require.Len(t, relations, 2)
			var baseline []byte
			for _, paired := range []bool{false, true} {
				graph, err := newSharedTimeGraph(model.timeGraphConfig("space"), grid)
				require.NoError(t, err)
				require.True(t, graph.topologyReversePair(relations))
				if paired {
					require.NoError(t, graph.applyRelationMatrixWithReverse(context.Background(), data, relations[0], &relations[1], nil, nil))
				} else {
					for _, r := range relations {
						require.NoError(t, graph.applyRelationMatrix(context.Background(), data, r, nil, nil))
					}
				}
				got, err := graph.findSharedTopologyDTO(context.Background(), grid, SharedTopologyQuery{SourceType: source, SourceMatcher: matcher, MaxHops: 10})
				require.NoError(t, err)
				wire, err := json.Marshal(got)
				require.NoError(t, err)
				if !paired {
					baseline = wire
				} else {
					require.Equal(t, string(baseline), string(wire), "numeric IDs, per-frame full dimensions and complete directional edges must match")
				}
				graph.maxEdges = 1
				require.False(t, graph.topologyReversePair(relations), "ordinary limits retain ordered separate applications")
				graph.Clean(context.Background())
			}
		})
	}
}
