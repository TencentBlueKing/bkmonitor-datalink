package v1beta3

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestTimeGraphQueryGenerationCases(t *testing.T) {
	tests := []struct {
		name       string
		config     *TimeGraphConfig
		info       cmdb.Matcher
		relation   cmdb.Relation
		start      time.Time
		end        time.Time
		step       time.Duration
		window     time.Duration
		wantMetric string
		wantWindow string
		wantStep   string
		wantFields []structured.ConditionField
	}{
		{
			name: "configured_static_metric_is_not_replaced_by_default_name",
			config: &TimeGraphConfig{Resource: []TimeGraphResourceConfig{
				{Name: "left", Index: cmdb.Index{"left_id"}}, {Name: "right", Index: cmdb.Index{"right_id"}},
			}, Relation: []TimeGraphRelationConfig{{
				Resources: []cmdb.Resource{"left", "right"}, RelationType: "left_to_right", MetricName: "left_to_right_flow", Category: string(RelationCategoryStatic),
			}}},
			info:     cmdb.Matcher{"left_id": "l1"},
			relation: cmdb.Relation{V: []cmdb.Resource{"left", "right"}, RelationType: "left_to_right", MetricName: "left_to_right_flow", Category: string(RelationCategoryStatic)},
			start:    time.Unix(100, 0), end: time.Unix(100, 0), step: time.Minute, window: 10 * time.Minute,
			wantMetric: "left_to_right_flow", wantWindow: "10m0s", wantStep: "1m0s",
			wantFields: []structured.ConditionField{{DimensionName: "left_id", Value: []string{"l1"}, Operator: structured.ConditionEqual}, {DimensionName: "right_id", Value: []string{""}, Operator: structured.ConditionNotEqual}},
		},
		{
			name: "dynamic_inbound_uses_to_prefix_for_source",
			config: &TimeGraphConfig{Resource: []TimeGraphResourceConfig{
				{Name: "service", Index: cmdb.Index{"id"}},
			}, Relation: []TimeGraphRelationConfig{{
				Resources: []cmdb.Resource{"service", "service"}, RelationType: "service_to_service", MetricName: "service_to_service_flow", Category: string(RelationCategoryDynamic),
			}}},
			info:     cmdb.Matcher{"id": "callee"},
			relation: cmdb.Relation{V: []cmdb.Resource{"service", "service"}, RelationType: "service_to_service", MetricName: "service_to_service_flow", Category: string(RelationCategoryDynamic), Direction: string(DirectionInbound)},
			start:    time.Unix(100, 0), end: time.Unix(200, 0), step: time.Minute, window: 10 * time.Minute,
			wantMetric: "service_to_service_flow", wantWindow: "10m0s", wantStep: "1m0s",
			wantFields: []structured.ConditionField{{DimensionName: "from_id", Value: []string{""}, Operator: structured.ConditionNotEqual}, {DimensionName: "to_id", Value: []string{"callee"}, Operator: structured.ConditionEqual}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tg := NewTimeGraphWithConfig(tc.config)
			query, err := tg.MakeQueryTsWithWindow(context.Background(), "space", tc.info, tc.start, tc.end, tc.step, tc.window, tc.relation)
			if err != nil {
				t.Fatal(err)
			}
			if query == nil || len(query.QueryList) != 1 {
				t.Fatalf("unexpected generated query: %+v", query)
			}
			generated := query.QueryList[0]
			if generated.FieldName != tc.wantMetric {
				t.Fatalf("metric mismatch: want=%q got=%q", tc.wantMetric, generated.FieldName)
			}
			if string(generated.TimeAggregation.Window) != tc.wantWindow || query.Step != tc.wantStep {
				t.Fatalf("time parameters mismatch: want window=%q step=%q got window=%q step=%q", tc.wantWindow, tc.wantStep, generated.TimeAggregation.Window, query.Step)
			}
			if len(generated.Conditions.FieldList) != len(tc.wantFields) {
				t.Fatalf("condition field count mismatch: want=%+v got=%+v", tc.wantFields, generated.Conditions.FieldList)
			}
			for i, want := range tc.wantFields {
				if !reflect.DeepEqual(want, generated.Conditions.FieldList[i]) {
					t.Fatalf("condition[%d] mismatch: want=%+v got=%+v", i, want, generated.Conditions.FieldList[i])
				}
			}
		})
	}
}
