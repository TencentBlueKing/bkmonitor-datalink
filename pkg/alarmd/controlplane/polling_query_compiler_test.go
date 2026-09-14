package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func pollingTestSource(raw string) PrimaryQuerySource {
	return PrimaryQuerySource{Identity: SourceIdentity{TenantID: "tenant", BusinessID: "2", SpaceScope: "bkcc__2"},
		StrategyID: "14", ItemID: "3", QueryMD5: "query", Expression: "a", QueryConfigs: []json.RawMessage{json.RawMessage(raw)}}
}

func TestPollingSourceQuerySemantics(t *testing.T) {
	planner, _ := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{})
	for _, tc := range []struct {
		name, config, table, field, dataSource, dimension, aggregation string
		delay                                                          int64
	}{
		{"custom", `{"data_source_label":"custom","data_type_label":"time_series","result_table_id":"Custom.Metric","metric_field":"value","alias":"a","agg_method":"AVG","agg_interval":60,"agg_dimension":["host"]}`, "custom.metric", "value", "", "host", "avg_over_time", 0},
		{"log-count", `{"data_source_label":"bk_log_search","data_type_label":"log","result_table_id":"7","agg_method":"COUNT","alias":"a","agg_interval":45,"agg_dimension":["host"]}`, "bklog_index_set_7", "_index", "bklog", "host", "count_over_time", 90},
		{"monitor-event-count", `{"data_source_label":"bk_monitor","data_type_label":"log","result_table_id":"system.event","agg_method":"COUNT","agg_interval":60,"agg_dimension":["host"]}`, "system.event.__default__", "event.count", "bkapm", "dimensions.host", "sum_over_time", 0},
		{"custom-event", `{"data_source_label":"custom","data_type_label":"event","result_table_id":"k8s_event","custom_event_name":"PodError","agg_interval":60,"agg_dimension":["namespace"]}`, "k8s_event", "_index", "bkapm", "dimensions.namespace", "count_over_time", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts, err := planner.CompilePrimaryQuery(context.Background(), pollingTestSource(tc.config))
			if err != nil {
				t.Fatal(err)
			}
			q := facts.QueryList[0]
			if q.TableID != tc.table || q.FieldName != tc.field || q.DataSource != tc.dataSource || q.Dimensions[0] != tc.dimension || q.TimeAggregation.Method != tc.aggregation || facts.QueryDelaySeconds != tc.delay {
				t.Fatalf("query=%+v delay=%d", q, facts.QueryDelaySeconds)
			}
			if tc.name == "custom-event" && (len(q.Conditions.Fields) != 2 || q.Conditions.Fields[1].Field != "dimensions.event_type" || q.Conditions.Fields[1].Operator != "ne") {
				t.Fatalf("custom constructor filters=%+v", q.Conditions)
			}
			if tc.name == "monitor-event-count" && facts.Normalization.DatasetContract.IdentityFields[0] != "host" {
				t.Fatalf("canonical identity=%+v", facts.Normalization)
			}
		})
	}
}

func TestPollingPromQLDynamicIdentityAndImmutableSourceFacts(t *testing.T) {
	planner, _ := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{})
	source := pollingTestSource(`{"data_source_label":"prometheus","data_type_label":"time_series","promql":"sum by (pod) (up)","agg_interval":60,"filter_dict":{"labels":{"job":"api"},"ignored":1}}`)
	facts, err := planner.CompilePrimaryQuery(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if facts.PromQL == nil || facts.PromQL.Match != "{job='api'}" || !facts.Normalization.DatasetContract.DynamicDimensions || len(facts.QueryList) > 0 || facts.MetricMerge != "" {
		t.Fatalf("facts=%+v", facts)
	}
	source.TimeDelaySeconds = 61
	delayed, err := planner.CompilePrimaryQuery(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if delayed.QueryDelaySeconds != 120 || delayed.QueryRevision == facts.QueryRevision {
		t.Fatalf("delayed=%+v", delayed)
	}
	facts.QueryRevision = ""
	facts.MetricMerge = "a"
	if _, err := execution.BuildQueryPlanFacts(facts); err == nil {
		t.Fatal("mixed query endpoints accepted")
	}
}

func TestPollingFTASourceConditionsRemainSeparate(t *testing.T) {
	storage := execution.QueryStorage{TableID: "fta.events", StorageID: "17", StorageType: "elasticsearch", DB: "bkfta_event_*_read", Measurement: "__default__", TimeField: execution.QueryTimeField{Name: "time", Type: "date", Unit: "millisecond"}, SourceType: "event"}
	planner, _ := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{FTAEventStorage: &storage})
	storage.DB = "mutated"
	source := pollingTestSource(`{"data_source_label":"bk_fta","data_type_label":"event","metric_field":"__EVENT_PLUGIN__zabbix","alias":"a","agg_interval":60,"agg_dimension":["tags.env"],"agg_condition":[{"key":"tags.env","method":"include","value":["prod"]}]}`)
	facts, err := planner.CompilePrimaryQuery(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	q := facts.QueryList[0]
	if q.FieldSemantics != "fta_event_tags/v1" || len(q.Conditions.Fields) != 1 || q.Conditions.Fields[0].Operator != "contains" || q.SourceConditions == nil || len(q.SourceConditions.Fields) != 3 {
		t.Fatalf("query=%+v", q)
	}
	if facts.TSDBMap["a"][0].DB != "bkfta_event_*_read" {
		t.Fatal("runtime routing was mutable")
	}
	if q.SourceConditions.Fields[2].Field != "plugin_id" || q.SourceConditions.Fields[2].Values[0].StringValue != "zabbix" {
		t.Fatalf("intrinsic=%+v", q.SourceConditions)
	}
	missing, _ := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{})
	_, err = missing.CompilePrimaryQuery(context.Background(), source)
	var failure *QueryPlanCompileError
	if !errors.As(err, &failure) || failure.Disposition != DispositionSourceIncomplete {
		t.Fatalf("missing route=%v", err)
	}
}

func TestPollingBKDataDirectBoundary(t *testing.T) {
	planner, _ := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{})
	source := pollingTestSource(`{"data_source_label":"bk_data","data_type_label":"time_series","result_table_id":"2_metric","metric_field":"value","alias":"a","agg_method":"AVG","agg_interval":60}`)
	if _, err := planner.CompilePrimaryQuery(context.Background(), source); err == nil {
		t.Fatal("direct BKSQL accepted")
	}
	source.Expression = "a + 1"
	facts, err := planner.CompilePrimaryQuery(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if facts.QueryList[0].TimeField != "dtEventTimeStamp" {
		t.Fatalf("query=%+v", facts.QueryList)
	}
}
