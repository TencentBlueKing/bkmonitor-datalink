package v1beta3

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func initTimeGraphQueryTestEnvironment() context.Context {
	metadata.InitMetadata()
	return metadata.InitHashID(context.Background())
}

type publicTimeGraphQueryCall struct {
	metric  string
	expr    string
	instant bool
	start   time.Time
	end     time.Time
	step    time.Duration
}

type publicTimeGraphVM struct {
	responses map[string]pl.Matrix
	failOn    string
	calls     []publicTimeGraphQueryCall
}

func (vm *publicTimeGraphVM) query(_ context.Context, queryTs *structured.QueryTs, expr string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
	metric := queryTs.QueryList[0].FieldName
	vm.calls = append(vm.calls, publicTimeGraphQueryCall{metric: metric, expr: expr, instant: instant, start: start, end: end, step: step})
	if response, ok := vm.responses[metric]; ok {
		if metric == vm.failOn {
			return nil, errors.New("public VM query failed")
		}
		return response, nil
	}
	return nil, fmt.Errorf("public VM query metric was not rendered: %s", metric)
}

func publicChainProvider() SchemaProvider {
	return contractSchemaProvider{
		resources: []ResourceType{"node", "middle", "target"},
		primary: map[ResourceType][]string{
			"node":   {"node_id"},
			"middle": {"middle_id"},
			"target": {"target_id"},
		},
		fields: map[ResourceType][]string{
			"node":   {"node_id"},
			"middle": {"middle_id"},
			"target": {"target_id"},
		},
		schemas: []RelationSchema{
			{RelationType: "node_to_middle", Category: RelationCategoryStatic, FromType: "node", ToType: "middle", MetricName: "node_to_middle_flow"},
			{RelationType: "middle_to_target", Category: RelationCategoryStatic, FromType: "middle", ToType: "target", MetricName: "middle_to_target_flow"},
		},
	}
}

func TestTimeGraphPublicQueryContractCases(t *testing.T) {
	const startSec int64 = 1700000000
	const timestampMS int64 = startSec * 1000
	start := time.Unix(startSec, 0)

	tests := []struct {
		name          string
		pathResources [][]cmdb.Resource
		responses     map[string]pl.Matrix
		failOn        string
		wantErr       string
		wantResults   []cmdb.PathResourcesResult
		wantCalls     []string
	}{
		{
			name: "auto_discovers_and_executes_multi_hop_path",
			responses: map[string]pl.Matrix{
				"node_to_middle_flow":   contractMatrix(map[string]string{"node_id": "n1", "middle_id": "m1"}, timestampMS),
				"middle_to_target_flow": contractMatrix(map[string]string{"middle_id": "m1", "target_id": "t1"}, timestampMS),
			},
			wantResults: []cmdb.PathResourcesResult{{Timestamp: timestampMS, TargetType: "target", Path: []cmdb.PathNode{
				{ResourceType: "node", Dimensions: cmdb.Matcher{"node_id": "n1"}},
				{ResourceType: "middle", Dimensions: cmdb.Matcher{"middle_id": "m1"}},
				{ResourceType: "target", Dimensions: cmdb.Matcher{"target_id": "t1"}},
			}}},
			wantCalls: []string{"node_to_middle_flow", "middle_to_target_flow"},
		},
		{
			name:          "unknown_requested_path_is_rejected_before_vm",
			pathResources: [][]cmdb.Resource{{"node", "missing", "target"}},
			wantErr:       `unknown path resource type "missing"`,
		},
		{
			name:   "external_vm_error_is_returned_from_public_entry",
			failOn: "middle_to_target_flow",
			responses: map[string]pl.Matrix{
				"node_to_middle_flow":   contractMatrix(map[string]string{"node_id": "n1", "middle_id": "m1"}, timestampMS),
				"middle_to_target_flow": contractMatrix(map[string]string{"middle_id": "m1", "target_id": "t1"}, timestampMS),
			},
			wantErr:   "public VM query failed",
			wantCalls: []string{"node_to_middle_flow", "middle_to_target_flow"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vm := &publicTimeGraphVM{responses: tc.responses, failOn: tc.failOn}
			model := &Model{schemaProvider: publicChainProvider(), timeGraphVMQuery: vm.query}
			ctx := initTimeGraphQueryTestEnvironment()
			got, err := model.QueryPathResources(
				ctx, "10m", "space", "1700000000", "node", []cmdb.Resource{"target"},
				tc.pathResources, cmdb.Matcher{"node_id": "n1"},
			)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				if len(tc.wantCalls) == 0 {
					require.Empty(t, vm.calls)
				} else {
					require.Len(t, vm.calls, len(tc.wantCalls))
					for i, metric := range tc.wantCalls {
						require.Equal(t, metric, vm.calls[i].metric)
					}
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantResults, got)
			require.Len(t, vm.calls, len(tc.wantCalls))
			for i, metric := range tc.wantCalls {
				require.Equal(t, metric, vm.calls[i].metric)
				require.Contains(t, vm.calls[i].expr, "count_over_time")
				require.True(t, vm.calls[i].instant)
				require.Equal(t, start.UTC().Truncate(5*time.Minute).Unix(), vm.calls[i].start.Unix())
				require.Equal(t, start.Unix(), vm.calls[i].end.Unix())
				require.Equal(t, 5*time.Minute, vm.calls[i].step)
			}
		})
	}
}

func TestTimeGraphPublicRangeQueryContract(t *testing.T) {
	const startSec int64 = 1700000000
	const timestampMS int64 = startSec * 1000
	vm := &publicTimeGraphVM{responses: map[string]pl.Matrix{
		"node_to_middle_flow":   contractMatrix(map[string]string{"node_id": "n1", "middle_id": "m1"}, timestampMS, timestampMS+60000, timestampMS+120000),
		"middle_to_target_flow": contractMatrix(map[string]string{"middle_id": "m1", "target_id": "t1"}, timestampMS, timestampMS+60000, timestampMS+120000),
	}}
	model := &Model{schemaProvider: publicChainProvider(), timeGraphVMQuery: vm.query}
	ctx := initTimeGraphQueryTestEnvironment()

	results, err := model.QueryPathResourcesRange(
		ctx, "10m", "space", "1m", "1700000000", "1700000120", "node", []cmdb.Resource{"target"},
		nil, cmdb.Matcher{"node_id": "n1"},
	)
	require.NoError(t, err)
	require.Len(t, results, 3)
	for i, result := range results {
		require.Equal(t, timestampMS+int64(i)*60000, result.Timestamp)
		require.Equal(t, []string{"node", "middle", "target"}, cmdb.PathResourceTypes(result.Path))
	}
	require.Len(t, vm.calls, 2)
	for _, call := range vm.calls {
		require.False(t, call.instant)
		require.Contains(t, call.expr, "count_over_time")
		require.Equal(t, 1*time.Minute, call.step)
		require.Equal(t, (time.Unix(startSec, 0).UTC().Truncate(time.Minute)).Unix(), call.start.Unix())
		require.Equal(t, startSec+120, call.end.Unix())
	}
}

func TestVectorToMatrixPreservesLabelsAndTimestamp(t *testing.T) {
	vector := pl.Vector{{
		Metric: labels.Labels{{Name: "node_id", Value: "n1"}},
		Point:  pl.Point{T: 100000, V: 1},
	}}
	matrix := vectorToMatrix(vector)
	require.Len(t, matrix, 1)
	require.Equal(t, vector[0].Metric, matrix[0].Metric)
	require.Equal(t, vector[0].Point, matrix[0].Points[0])
}
