package v1beta3

import (
	"context"
	"fmt"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func sharedTopologyQueryProvider() SchemaProvider {
	return contractSchemaProvider{
		resources: []ResourceType{"source", "middle", "target"},
		primary: map[ResourceType][]string{
			"source": {"source_id"},
			"middle": {"middle_id"},
			"target": {"target_id"},
		},
		fields: map[ResourceType][]string{
			"source": {"source_id"},
			"middle": {"middle_id"},
			"target": {"target_id"},
		},
		schemas: []RelationSchema{
			{RelationType: "source_to_middle", Category: RelationCategoryStatic, FromType: "source", ToType: "middle", IsDirectional: true, MetricName: "source_middle_flow"},
			{RelationType: "middle_to_target", Category: RelationCategoryStatic, FromType: "middle", ToType: "target", IsDirectional: true, MetricName: "middle_target_flow"},
		},
	}
}

func sharedTopologyQueryModel(responses map[string]pl.Matrix) *Model {
	model := &Model{
		schemaProvider:          sharedTopologyQueryProvider(),
		timeGraphQueryReference: timeGraphTestQueryReference,
	}
	model.timeGraphVMQuery = func(_ context.Context, queryTs *structured.QueryTs, _ string, _ bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
		field := queryTs.QueryList[0].FieldName
		response, ok := responses[field]
		if !ok {
			return nil, fmt.Errorf("unexpected topology query metric %q", field)
		}
		// 模拟后端仅返回实际请求网格内的样本，不让 instant 测试混入 range 数据。
		matrix := make(pl.Matrix, 0, len(response))
		for _, series := range response {
			filtered := pl.Series{Metric: series.Metric}
			for _, point := range series.Points {
				if point.T >= start.UnixMilli() && point.T <= end.UnixMilli() && (point.T-start.UnixMilli())%step.Milliseconds() == 0 {
					filtered.Points = append(filtered.Points, point)
				}
			}
			if len(filtered.Points) > 0 {
				matrix = append(matrix, filtered)
			}
		}
		return matrix, nil
	}
	return model
}

func TestQuerySharedTopologyCases(t *testing.T) {
	responses := map[string]pl.Matrix{
		"source_info_relation": contractMatrix(map[string]string{"source_id": "a"}, 1700000000000, 1700000100000, 1700000200000),
		"source_middle_flow":   contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, 1700000000000, 1700000100000),
		"middle_target_flow":   contractMatrix(map[string]string{"middle_id": "b", "target_id": "c"}, 1700000100000, 1700000200000),
	}
	tests := []struct {
		name          string
		request       cmdb.SharedTopologyQuery
		wantPoints    int
		wantStep      string
		wantTargetAt  map[int]bool
		wantPartialAt []int
		wantErr       string
	}{
		{
			name: "range 使用统一时间网格",
			request: cmdb.SharedTopologyQuery{
				SpaceUID:   "space",
				StartTime:  1700000000,
				EndTime:    1700000200,
				Step:       "100s",
				SourceType: "source",
				SourceInfo: cmdb.Matcher{"source_id": "a"},
				MaxHops:    2,
			},
			wantPoints:   3,
			wantStep:     "1m40s",
			wantTargetAt: map[int]bool{0: false, 1: true, 2: false},
		},
		{
			name: "目标类型只过滤输出不改变遍历",
			request: cmdb.SharedTopologyQuery{
				SpaceUID:    "space",
				StartTime:   1700000000,
				EndTime:     1700000200,
				Step:        "100s",
				SourceType:  "source",
				SourceInfo:  cmdb.Matcher{"source_id": "a"},
				TargetTypes: []cmdb.Resource{"target"},
				MaxHops:     2,
			},
			wantPoints:   3,
			wantStep:     "1m40s",
			wantTargetAt: map[int]bool{0: false, 1: true, 2: false},
		},
		{
			name: "instant 是单点网格",
			request: cmdb.SharedTopologyQuery{
				SpaceUID:   "space",
				Timestamp:  1700000000,
				SourceType: "source",
				SourceInfo: cmdb.Matcher{"source_id": "a"},
				MaxHops:    1,
			},
			wantPoints:   1,
			wantStep:     "0s",
			wantTargetAt: map[int]bool{0: false},
		},
		{
			name: "超过配置点数明确拒绝",
			request: cmdb.SharedTopologyQuery{
				SpaceUID:   "space",
				StartTime:  1700000000,
				EndTime:    1700006000,
				Step:       "100s",
				SourceType: "source",
			},
			wantErr: "topology time grid contains",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := sharedTopologyQueryModel(responses)
			result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), tt.request)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantPoints, result.PointCount)
			require.Equal(t, tt.wantStep, result.Step)
			require.Len(t, result.Snapshots, tt.wantPoints)
			for index, want := range tt.wantTargetAt {
				found := false
				for _, node := range result.Snapshots[index].Nodes {
					if node.ResourceType == "target" && node.Dimensions["target_id"] == "c" {
						found = true
						break
					}
				}
				require.Equal(t, want, found, "目标节点时间点 %d", index)
			}
		})
	}
}

func TestQuerySharedTopologyPartialCases(t *testing.T) {
	tests := []struct {
		name          string
		partialMetric string
		wantPartial   bool
	}{
		{name: "关系查询 partial", partialMetric: "middle_target_flow", wantPartial: true},
		{name: "所有关系查询完整", wantPartial: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			responses := map[string]pl.Matrix{
				"source_info_relation": contractMatrix(map[string]string{"source_id": "a"}, 1700000000000, 1700000100000, 1700000200000),
				"source_middle_flow":   contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, 1700000000000, 1700000100000),
				"middle_target_flow":   contractMatrix(map[string]string{"middle_id": "b", "target_id": "c"}, 1700000100000, 1700000200000),
			}
			model := sharedTopologyQueryModel(responses)
			model.timeGraphVMQueryWithPartial = func(ctx context.Context, queryTs *structured.QueryTs, expr string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, bool, error) {
				matrix, err := model.timeGraphVMQuery(ctx, queryTs, expr, instant, start, end, step)
				return matrix, queryTs.QueryList[0].FieldName == tt.partialMetric, err
			}
			result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{
				SpaceUID:   "space",
				StartTime:  1700000000,
				EndTime:    1700000200,
				Step:       "100s",
				SourceType: "source",
				SourceInfo: cmdb.Matcher{"source_id": "a"},
				MaxHops:    2,
			})
			require.NoError(t, err)
			for _, snapshot := range result.Snapshots {
				require.Equal(t, tt.wantPartial, snapshot.Partial)
				if tt.wantPartial {
					require.Equal(t, "backend_partial", snapshot.PartialReason)
				}
			}
		})
	}
}
