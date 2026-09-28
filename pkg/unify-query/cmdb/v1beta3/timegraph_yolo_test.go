package v1beta3

import (
	"context"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestYoloBypassesDataLimitsAndRestoresDefaults(t *testing.T) {
	old := yoloMode
	t.Cleanup(func() { yoloMode = old })
	limits := []struct {
		name string
		get  func() int
	}{
		{"range points", effectiveMaxRangePoints},
		{"targets", effectiveMaxTargets},
		{"nodes", effectiveMaxGraphNodes},
		{"edges", effectiveMaxGraphEdges},
		{"results", effectiveMaxGraphResults},
		{"node infos", effectiveMaxGraphNodeInfos},
		{"topology points", effectiveMaxSharedTopologyPoints},
		{"request bytes", TopologyRequestByteLimit},
		{"query items", TopologyQueryLimit},
		{"output bytes", TopologyOutputByteLimit},
		{"matrix points", func() int { return effectiveTimeGraphLimit(MaxSharedTopologyMatrixPoints, 1000000) }},
		{"backend bytes", func() int { return effectiveTimeGraphLimit(MaxSharedTopologyBackendBytes, 16*1024*1024) }},
		{"output elements", func() int { return effectiveTimeGraphLimit(MaxSharedTopologyOutputElements, 200000) }},
	}
	for _, enabled := range []bool{false, true, false} {
		yoloMode = enabled
		for _, limit := range limits {
			if enabled {
				require.Zero(t, limit.get(), limit.name)
			} else {
				require.Positive(t, limit.get(), limit.name)
			}
		}
		_, gridErr := NewTopologyGrid(sharedGraphTestTimes(129))
		_, rangeErr := validateRangeBuckets(0, 11000*1000, 1000)
		targetErr := validateTargetCount(5001)
		if enabled {
			require.NoError(t, gridErr)
			require.NoError(t, rangeErr)
			require.NoError(t, targetErr)
		} else {
			require.Error(t, gridErr)
			require.Error(t, rangeErr)
			require.Error(t, targetErr)
		}
	}
}

func TestYoloSharedTopologyBypassesConfiguredBudgets(t *testing.T) {
	for _, test := range []struct {
		name  string
		limit *int
	}{
		{"graph nodes", &MaxGraphNodes},
		{"graph edges", &MaxGraphEdges},
		{"node attributes", &MaxGraphNodeInfos},
		{"matrix points", &MaxSharedTopologyMatrixPoints},
		{"output elements", &MaxSharedTopologyOutputElements},
		{"output bytes", &MaxSharedTopologyOutputBytes},
	} {
		t.Run(test.name, func(t *testing.T) {
			oldLimit, oldYolo := *test.limit, yoloMode
			t.Cleanup(func() { *test.limit, yoloMode = oldLimit, oldYolo })
			*test.limit = 1
			timestamps := sharedGraphTestTimes(3)
			model := sharedTopologyQueryModel(map[string]pl.Matrix{
				"source_info_relation": contractMatrix(map[string]string{"source_id": "a"}, timestamps...),
				"source_middle_flow":   contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, timestamps...),
				"middle_target_flow":   nil,
			})
			for _, enabled := range []bool{false, true, false} {
				yoloMode = enabled
				result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{
					SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"},
					StartTime: timestamps[0], EndTime: timestamps[2], Step: "1s", MaxHops: 1,
				})
				if !enabled {
					require.Error(t, err)
					continue
				}
				require.NoError(t, err)
				require.Len(t, result.Snapshots, 3)
				for _, snapshot := range result.Snapshots {
					require.Len(t, snapshot.Nodes, 2)
					require.Len(t, snapshot.Edges, 1)
				}
			}
		})
	}
}

func TestYoloDoesNotInstallBackendResponseBudget(t *testing.T) {
	oldYolo := yoloMode
	t.Cleanup(func() { yoloMode = oldYolo })
	for _, enabled := range []bool{false, true} {
		yoloMode = enabled
		model := sharedTopologyQueryModel(nil)
		calls := 0
		model.timeGraphVMQuery = func(ctx context.Context, _ *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
			calls++
			if enabled {
				require.Zero(t, metadata.BackendResponseLimit(ctx))
			} else {
				require.Positive(t, metadata.BackendResponseLimit(ctx))
			}
			return nil, nil
		}
		_, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{
			SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, Timestamp: 1700000000,
		})
		require.NoError(t, err)
		require.Positive(t, calls)
	}
}
