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

func timeGraphTestQueryReference(ctx context.Context, queryTs *structured.QueryTs) (metadata.QueryReference, error) {
	if err := queryTs.ToTime(ctx); err != nil {
		return nil, err
	}
	reference := make(metadata.QueryReference, len(queryTs.QueryList))
	for _, query := range queryTs.QueryList {
		if query == nil {
			continue
		}
		reference[query.ReferenceName] = append(reference[query.ReferenceName], &metadata.QueryMetric{
			ReferenceName: query.ReferenceName,
			MetricName:    query.FieldName,
			QueryList: metadata.QueryList{{
				VmRt:        "timegraph_test",
				VmCondition: metadata.VmCondition(fmt.Sprintf(`__name__=%q`, query.FieldName)),
				StorageName: "timegraph_test",
				TableID:     string(query.TableID),
			}},
		})
	}
	metadata.SetQueryReference(ctx, reference)
	return reference, nil
}

type publicTimeGraphQueryCall struct {
	metric     string
	expr       string
	instant    bool
	start      time.Time
	end        time.Time
	step       time.Duration
	window     string
	conditions structured.Conditions
	aggregate  structured.AggregateMethodList
}

type publicTimeGraphQueryWant struct {
	metric     string
	expr       string
	instant    bool
	start      int64
	end        int64
	step       time.Duration
	window     string
	conditions structured.Conditions
	aggregate  structured.AggregateMethodList
}

type publicTimeGraphVM struct {
	responses map[string]pl.Matrix
	failOn    string
	calls     []publicTimeGraphQueryCall
}

func (vm *publicTimeGraphVM) query(ctx context.Context, queryTs *structured.QueryTs, expr string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
	metric := queryTs.QueryList[0].FieldName
	query := queryTs.QueryList[0]
	vm.calls = append(vm.calls, publicTimeGraphQueryCall{
		metric:     metric,
		expr:       expr,
		instant:    instant,
		start:      start,
		end:        end,
		step:       step,
		window:     string(query.TimeAggregation.Window),
		conditions: cloneContractConditions(query.Conditions),
		aggregate:  append(structured.AggregateMethodList(nil), query.AggregateMethodList...),
	})
	if metadata.GetExpand(ctx) == nil {
		return nil, errors.New("timegraph VM query was called without SetExpand preparation")
	}
	if response, ok := vm.responses[metric]; ok {
		if metric == vm.failOn {
			return nil, errors.New("public VM query failed")
		}
		return response, nil
	}
	return nil, fmt.Errorf("public VM query metric was not rendered: %s", metric)
}

func requirePublicTimeGraphCalls(t *testing.T, got []publicTimeGraphQueryCall, want []publicTimeGraphQueryWant) {
	t.Helper()
	require.Len(t, got, len(want))
	for i, expected := range want {
		actual := got[i]
		require.Equal(t, expected.metric, actual.metric, "call %d metric", i)
		require.Equal(t, expected.expr, actual.expr, "call %d expression", i)
		require.Equal(t, expected.instant, actual.instant, "call %d instant", i)
		require.Equal(t, expected.start, actual.start.Unix(), "call %d start", i)
		require.Equal(t, expected.end, actual.end.Unix(), "call %d end", i)
		require.Equal(t, expected.step, actual.step, "call %d step", i)
		require.Equal(t, expected.window, actual.window, "call %d lookback", i)
		require.Equal(t, expected.conditions, actual.conditions, "call %d conditions", i)
		require.Equal(t, expected.aggregate, actual.aggregate, "call %d aggregate", i)
	}
}

func publicStaticRelationConditions(sourceField, sourceValue, targetField string) structured.Conditions {
	fields := []structured.ConditionField{
		{DimensionName: sourceField, Value: []string{sourceValue}, Operator: structured.ConditionEqual},
		{DimensionName: targetField, Value: []string{""}, Operator: structured.ConditionNotEqual},
	}
	if sourceValue == "" {
		fields[0].Operator = structured.ConditionNotEqual
	}
	if fields[0].DimensionName > fields[1].DimensionName {
		fields[0], fields[1] = fields[1], fields[0]
	}
	return structured.Conditions{
		FieldList:     fields,
		ConditionList: []string{structured.ConditionAnd},
	}
}

func publicStaticRelationAggregate(dimensions ...string) structured.AggregateMethodList {
	return structured.AggregateMethodList{{Method: structured.COUNT, Dimensions: dimensions}}
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

func publicSourceExpandProvider() SchemaProvider {
	return contractSchemaProvider{
		resources: []ResourceType{"node", "middle"},
		primary: map[ResourceType][]string{
			"node":   {"node_id"},
			"middle": {"middle_id"},
		},
		fields: map[ResourceType][]string{
			"node":   {"node_id", "zone"},
			"middle": {"middle_id", "version"},
		},
		schemas: []RelationSchema{
			{RelationType: "node_to_middle", Category: RelationCategoryStatic, FromType: "node", ToType: "middle", MetricName: "node_to_middle_flow"},
		},
	}
}

type publicSourceExpandVM struct {
	vm          publicTimeGraphVM
	allowedZone string
}

func (vm *publicSourceExpandVM) query(ctx context.Context, queryTs *structured.QueryTs, expr string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
	matrix, err := vm.vm.query(ctx, queryTs, expr, instant, start, end, step)
	if err != nil {
		return nil, err
	}
	if queryTs.QueryList[0].FieldName != "node_info_relation" {
		return matrix, nil
	}
	for _, field := range queryTs.QueryList[0].Conditions.FieldList {
		if field.DimensionName == "zone" && (len(field.Value) == 0 || field.Value[0] != vm.allowedZone) {
			return pl.Matrix{}, nil
		}
	}
	return matrix, nil
}

func TestTimeGraphPublicQueryContractCases(t *testing.T) {
	const startSec int64 = 1700000000
	const timestampMS int64 = startSec * 1000
	start := time.Unix(startSec, 0)

	tests := []struct {
		name          string
		lookBackDelta string
		pathResources [][]cmdb.Resource
		responses     map[string]pl.Matrix
		failOn        string
		wantErr       string
		wantResults   []cmdb.PathResourcesResult
		wantCalls     []publicTimeGraphQueryWant
	}{
		{
			name:          "auto_discovers_and_executes_multi_hop_path",
			lookBackDelta: "10m",
			responses: map[string]pl.Matrix{
				"node_to_middle_flow":   contractMatrix(map[string]string{"node_id": "n1", "middle_id": "m1"}, timestampMS),
				"middle_to_target_flow": contractMatrix(map[string]string{"middle_id": "m1", "target_id": "t1"}, timestampMS),
			},
			wantResults: []cmdb.PathResourcesResult{{Timestamp: timestampMS, TargetType: "target", Path: []cmdb.PathNode{
				{ResourceType: "node", Dimensions: cmdb.Matcher{"node_id": "n1"}},
				{ResourceType: "middle", Dimensions: cmdb.Matcher{"middle_id": "m1"}},
				{ResourceType: "target", Dimensions: cmdb.Matcher{"target_id": "t1"}},
			}}},
			wantCalls: []publicTimeGraphQueryWant{
				{
					metric: "node_to_middle_flow", expr: "count by (middle_id, node_id) (count_over_time(a[10m]))", instant: true,
					start: start.UTC().Truncate(5 * time.Minute).Unix(), end: startSec, step: 5 * time.Minute, window: "10m0s",
					conditions: publicStaticRelationConditions("node_id", "n1", "middle_id"),
					aggregate:  publicStaticRelationAggregate("middle_id", "node_id"),
				},
				{
					metric: "middle_to_target_flow", expr: "count by (middle_id, target_id) (count_over_time(a[10m]))", instant: true,
					start: start.UTC().Truncate(5 * time.Minute).Unix(), end: startSec, step: 5 * time.Minute, window: "10m0s",
					conditions: publicStaticRelationConditions("middle_id", "", "target_id"),
					aggregate:  publicStaticRelationAggregate("middle_id", "target_id"),
				},
			},
		},
		{
			name:          "default_lookback_is_rendered_in_external_queries",
			lookBackDelta: "",
			responses: map[string]pl.Matrix{
				"node_to_middle_flow":   contractMatrix(map[string]string{"node_id": "n1", "middle_id": "m1"}, timestampMS),
				"middle_to_target_flow": contractMatrix(map[string]string{"middle_id": "m1", "target_id": "t1"}, timestampMS),
			},
			wantResults: []cmdb.PathResourcesResult{{Timestamp: timestampMS, TargetType: "target", Path: []cmdb.PathNode{
				{ResourceType: "node", Dimensions: cmdb.Matcher{"node_id": "n1"}},
				{ResourceType: "middle", Dimensions: cmdb.Matcher{"middle_id": "m1"}},
				{ResourceType: "target", Dimensions: cmdb.Matcher{"target_id": "t1"}},
			}}},
			wantCalls: []publicTimeGraphQueryWant{
				{
					metric: "node_to_middle_flow", expr: "count by (middle_id, node_id) (count_over_time(a[1d]))", instant: true,
					start: start.UTC().Truncate(5 * time.Minute).Unix(), end: startSec, step: 5 * time.Minute, window: "24h0m0s",
					conditions: publicStaticRelationConditions("node_id", "n1", "middle_id"),
					aggregate:  publicStaticRelationAggregate("middle_id", "node_id"),
				},
				{
					metric: "middle_to_target_flow", expr: "count by (middle_id, target_id) (count_over_time(a[1d]))", instant: true,
					start: start.UTC().Truncate(5 * time.Minute).Unix(), end: startSec, step: 5 * time.Minute, window: "24h0m0s",
					conditions: publicStaticRelationConditions("middle_id", "", "target_id"),
					aggregate:  publicStaticRelationAggregate("middle_id", "target_id"),
				},
			},
		},
		{
			name:          "unknown_requested_path_is_rejected_before_vm",
			pathResources: [][]cmdb.Resource{{"node", "missing", "target"}},
			wantErr:       `unknown path resource type "missing"`,
		},
		{
			name:          "empty_external_response_returns_empty_results",
			lookBackDelta: "10m",
			responses: map[string]pl.Matrix{
				"node_to_middle_flow":   {},
				"middle_to_target_flow": {},
			},
			wantResults: []cmdb.PathResourcesResult{},
			wantCalls: []publicTimeGraphQueryWant{
				{
					metric: "node_to_middle_flow", expr: "count by (middle_id, node_id) (count_over_time(a[10m]))", instant: true,
					start: start.UTC().Truncate(5 * time.Minute).Unix(), end: startSec, step: 5 * time.Minute, window: "10m0s",
					conditions: publicStaticRelationConditions("node_id", "n1", "middle_id"),
					aggregate:  publicStaticRelationAggregate("middle_id", "node_id"),
				},
				{
					metric: "middle_to_target_flow", expr: "count by (middle_id, target_id) (count_over_time(a[10m]))", instant: true,
					start: start.UTC().Truncate(5 * time.Minute).Unix(), end: startSec, step: 5 * time.Minute, window: "10m0s",
					conditions: publicStaticRelationConditions("middle_id", "", "target_id"),
					aggregate:  publicStaticRelationAggregate("middle_id", "target_id"),
				},
			},
		},
		{
			name:          "external_vm_error_is_returned_from_public_entry",
			lookBackDelta: "10m",
			failOn:        "middle_to_target_flow",
			responses: map[string]pl.Matrix{
				"node_to_middle_flow":   contractMatrix(map[string]string{"node_id": "n1", "middle_id": "m1"}, timestampMS),
				"middle_to_target_flow": contractMatrix(map[string]string{"middle_id": "m1", "target_id": "t1"}, timestampMS),
			},
			wantErr: "public VM query failed",
			wantCalls: []publicTimeGraphQueryWant{
				{
					metric: "node_to_middle_flow", expr: "count by (middle_id, node_id) (count_over_time(a[10m]))", instant: true,
					start: start.UTC().Truncate(5 * time.Minute).Unix(), end: startSec, step: 5 * time.Minute, window: "10m0s",
					conditions: publicStaticRelationConditions("node_id", "n1", "middle_id"),
					aggregate:  publicStaticRelationAggregate("middle_id", "node_id"),
				},
				{
					metric: "middle_to_target_flow", expr: "count by (middle_id, target_id) (count_over_time(a[10m]))", instant: true,
					start: start.UTC().Truncate(5 * time.Minute).Unix(), end: startSec, step: 5 * time.Minute, window: "10m0s",
					conditions: publicStaticRelationConditions("middle_id", "", "target_id"),
					aggregate:  publicStaticRelationAggregate("middle_id", "target_id"),
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vm := &publicTimeGraphVM{responses: tc.responses, failOn: tc.failOn}
			model := &Model{
				schemaProvider:          publicChainProvider(),
				timeGraphVMQuery:        vm.query,
				timeGraphQueryReference: timeGraphTestQueryReference,
			}
			ctx := initTimeGraphQueryTestEnvironment()
			got, err := model.QueryPathResources(
				ctx, tc.lookBackDelta, "space", "1700000000", "node", []cmdb.Resource{"target"},
				tc.pathResources, cmdb.Matcher{"node_id": "n1"},
			)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				if len(tc.wantCalls) == 0 {
					require.Empty(t, vm.calls)
				} else {
					requirePublicTimeGraphCalls(t, vm.calls, tc.wantCalls)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantResults, got)
			requirePublicTimeGraphCalls(t, vm.calls, tc.wantCalls)
		})
	}
}

func publicChainResults(timestamps ...int64) []cmdb.PathResourcesResult {
	results := make([]cmdb.PathResourcesResult, 0, len(timestamps))
	for _, timestamp := range timestamps {
		results = append(results, cmdb.PathResourcesResult{
			Timestamp:  timestamp,
			TargetType: "target",
			Path: []cmdb.PathNode{
				{ResourceType: "node", Dimensions: cmdb.Matcher{"node_id": "n1"}},
				{ResourceType: "middle", Dimensions: cmdb.Matcher{"middle_id": "m1"}},
				{ResourceType: "target", Dimensions: cmdb.Matcher{"target_id": "t1"}},
			},
		})
	}
	return results
}

func TestTimeGraphPublicRangeQueryContract(t *testing.T) {
	const (
		step             = time.Minute
		lookback         = "10m0s"
		nodeMiddleExpr   = "count by (middle_id, node_id) (count_over_time(a[10m]))"
		middleTargetExpr = "count by (middle_id, target_id) (count_over_time(a[10m]))"
	)
	tests := []struct {
		name        string
		startSec    int64
		endSec      int64
		pointsSec   []int64
		wantResults []cmdb.PathResourcesResult
		wantCalls   []publicTimeGraphQueryWant
	}{
		{
			name:      "aligned_start_uses_start_as_first_evaluation_point",
			startSec:  1700000040,
			endSec:    1700000160,
			pointsSec: []int64{1700000040, 1700000100, 1700000160},
		},
		{
			name:      "unaligned_start_is_truncated_before_range_query",
			startSec:  1700000000,
			endSec:    1700000120,
			pointsSec: []int64{1699999980, 1700000040, 1700000100},
		},
	}

	for i := range tests {
		tests[i].wantResults = publicChainResults(
			int64(tests[i].pointsSec[0])*1000,
			int64(tests[i].pointsSec[1])*1000,
			int64(tests[i].pointsSec[2])*1000,
		)
		alignedStart := time.Unix(tests[i].startSec, 0).UTC().Truncate(step).Unix()
		tests[i].wantCalls = []publicTimeGraphQueryWant{
			{
				metric: "node_to_middle_flow", expr: nodeMiddleExpr, instant: false,
				start: alignedStart, end: tests[i].endSec, step: step, window: lookback,
				conditions: publicStaticRelationConditions("node_id", "n1", "middle_id"),
				aggregate:  publicStaticRelationAggregate("middle_id", "node_id"),
			},
			{
				metric: "middle_to_target_flow", expr: middleTargetExpr, instant: false,
				start: alignedStart, end: tests[i].endSec, step: step, window: lookback,
				conditions: publicStaticRelationConditions("middle_id", "", "target_id"),
				aggregate:  publicStaticRelationAggregate("middle_id", "target_id"),
			},
		}
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pointMS := make([]int64, 0, len(tc.pointsSec))
			for _, point := range tc.pointsSec {
				pointMS = append(pointMS, point*1000)
			}
			vm := &publicTimeGraphVM{responses: map[string]pl.Matrix{
				"node_to_middle_flow":   contractMatrix(map[string]string{"node_id": "n1", "middle_id": "m1"}, pointMS...),
				"middle_to_target_flow": contractMatrix(map[string]string{"middle_id": "m1", "target_id": "t1"}, pointMS...),
			}}
			model := &Model{
				schemaProvider:          publicChainProvider(),
				timeGraphVMQuery:        vm.query,
				timeGraphQueryReference: timeGraphTestQueryReference,
			}
			ctx := initTimeGraphQueryTestEnvironment()

			results, err := model.QueryPathResourcesRange(
				ctx, "10m", "space", "1m", fmt.Sprint(tc.startSec), fmt.Sprint(tc.endSec),
				"node", []cmdb.Resource{"target"}, nil, cmdb.Matcher{"node_id": "n1"},
			)
			require.NoError(t, err)
			require.Equal(t, tc.wantResults, results)
			requirePublicTimeGraphCalls(t, vm.calls, tc.wantCalls)
		})
	}
}

func TestTimeGraphPublicSourceExpandAndTargetInfoCases(t *testing.T) {
	const timestampMS int64 = 1700000000000
	tests := []struct {
		name             string
		sourceExpandInfo cmdb.Matcher
		targetInfo       pl.Matrix
		wantMatchers     cmdb.Matchers
	}{
		{
			name:             "source_expand_hit_returns_target_info",
			sourceExpandInfo: cmdb.Matcher{"zone": "east"},
			targetInfo:       contractMatrix(map[string]string{"middle_id": "m1", "version": "v1"}, timestampMS),
			wantMatchers:     cmdb.Matchers{{"middle_id": "m1", "version": "v1"}},
		},
		{
			name:             "source_expand_miss_returns_empty_result",
			sourceExpandInfo: cmdb.Matcher{"zone": "west"},
			targetInfo:       contractMatrix(map[string]string{"middle_id": "m1", "version": "v1"}, timestampMS),
			wantMatchers:     cmdb.Matchers{},
		},
		{
			name:             "missing_target_info_keeps_primary_identity",
			sourceExpandInfo: cmdb.Matcher{"zone": "east"},
			targetInfo:       pl.Matrix{},
			wantMatchers:     cmdb.Matchers{{"middle_id": "m1"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vm := &publicSourceExpandVM{
				vm: publicTimeGraphVM{responses: map[string]pl.Matrix{
					"node_info_relation":   contractMatrix(map[string]string{"node_id": "n1", "zone": "east"}, timestampMS),
					"node_to_middle_flow":  contractMatrix(map[string]string{"node_id": "n1", "middle_id": "m1"}, timestampMS),
					"middle_info_relation": tc.targetInfo,
				}},
				allowedZone: "east",
			}
			model := &Model{
				schemaProvider:          publicSourceExpandProvider(),
				timeGraphVMQuery:        vm.query,
				timeGraphQueryReference: timeGraphTestQueryReference,
			}
			model.SetTimeGraphResolver(func(context.Context, string) (cmdb.CMDB, error) { return model, nil })

			ctx := initTimeGraphQueryTestEnvironment()
			source, sourceMatcher, paths, target, matchers, err := model.QueryResourceMatcher(
				ctx, "10m", "space", "1700000000", "middle", "node",
				cmdb.Matcher{"node_id": "n1"}, tc.sourceExpandInfo, true, nil,
			)
			require.NoError(t, err)
			require.Equal(t, cmdb.Resource("node"), source)
			require.Equal(t, cmdb.Matcher{"node_id": "n1", "zone": tc.sourceExpandInfo["zone"]}, sourceMatcher)
			require.Equal(t, []string{"node", "middle"}, paths)
			require.Equal(t, cmdb.Resource("middle"), target)
			require.Equal(t, tc.wantMatchers, matchers)
		})
	}
}

func TestTimeGraphPublicQueryParameterValidation(t *testing.T) {
	tests := []struct {
		name          string
		lookBackDelta string
		step          string
		start         string
		end           string
		wantErr       string
		rangeQuery    bool
	}{
		{name: "zero_lookback_is_rejected", lookBackDelta: "0s", wantErr: "look back delta must be positive"},
		{name: "negative_lookback_is_rejected", lookBackDelta: "-1m", wantErr: "look back delta must be positive"},
		{name: "malformed_lookback_is_rejected", lookBackDelta: "not-a-duration", wantErr: "parse look back delta"},
		{name: "zero_step_is_rejected", step: "0s", start: "1700000000", end: "1700000060", wantErr: "step must be positive", rangeQuery: true},
		{name: "malformed_step_is_rejected", step: "not-a-duration", start: "1700000000", end: "1700000060", wantErr: "parse step", rangeQuery: true},
		{name: "reversed_range_is_rejected", step: "1m", start: "1700000060", end: "1700000000", wantErr: "start_time must be less than or equal to end_time", rangeQuery: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model := &Model{schemaProvider: publicChainProvider()}
			var err error
			if tc.rangeQuery {
				_, err = model.QueryPathResourcesRange(
					context.Background(), tc.lookBackDelta, "space", tc.step, tc.start, tc.end,
					"node", []cmdb.Resource{"middle"}, [][]cmdb.Resource{{"node", "middle"}}, cmdb.Matcher{"node_id": "n1"},
				)
			} else {
				_, err = model.QueryPathResources(
					context.Background(), tc.lookBackDelta, "space", "1700000000", "node", []cmdb.Resource{"middle"},
					[][]cmdb.Resource{{"node", "middle"}}, cmdb.Matcher{"node_id": "n1"},
				)
			}
			require.ErrorContains(t, err, tc.wantErr)
		})
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
