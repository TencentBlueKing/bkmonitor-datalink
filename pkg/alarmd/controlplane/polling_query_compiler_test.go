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
		{"log-count", `{"data_source_label":"bk_log_search","data_type_label":"log","index_set_id":7,"agg_method":"COUNT","alias":"a","agg_interval":45,"agg_dimension":["host"]}`, "bklog_index_set_7", "_index", "bklog", "host", "count_over_time", 90},
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
	source := pollingTestSource(`{"data_source_label":"bk_fta","data_type_label":"event","alert_name":"__EVENT_PLUGIN__zabbix","alias":"a","agg_interval":60,"agg_dimension":["tags.env"],"agg_condition":[{"key":"tags.env","method":"include","value":["prod"]}]}`)
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
	storage.TimeField.Unit = "second"
	invalid, _ := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{FTAEventStorage: &storage})
	_, err = invalid.CompilePrimaryQuery(context.Background(), source)
	if !errors.As(err, &failure) || failure.Reason != "QUERY_FTA_EVENT_STORAGE_INVALID" {
		t.Fatalf("wrong ES bucket units=%v", err)
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

func TestFTATypedRangeBoundsBeforeStringWire(t *testing.T) {
	conditions, err := compileFTAConditions([]legacyCondition{
		{Key: "tags.x", Method: "gt", Value: json.RawMessage(`[2,10]`)},
		{Key: "tags.x", Method: "lt", Value: json.RawMessage(`[2,10]`)},
		{Key: "time", Method: "gte", Value: json.RawMessage(`[1700000000000]`)},
		{Key: "tags.x", Method: "gt", Value: json.RawMessage(`["2","10"]`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"10", "2", "1700000000", "2"} {
		if conditions.Fields[i].Values[0].StringValue != want {
			t.Fatalf("field %d=%+v want %s", i, conditions.Fields[i], want)
		}
	}
}

func TestFTARejectsMismatchedMinuteBuckets(t *testing.T) {
	route := execution.QueryStorage{TableID: "events", StorageID: "17", StorageType: "elasticsearch", DB: "bkfta_event_*_read", Measurement: "__default__", TimeField: execution.QueryTimeField{Name: "time", Type: "date", Unit: "millisecond"}}
	planner, _ := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{FTAEventStorage: &route})
	for _, interval := range []int64{30, 90, 120, 0} {
		config, _ := json.Marshal(map[string]any{"data_source_label": "bk_fta", "data_type_label": "event", "alert_name": "CPUHigh", "agg_interval": interval})
		facts, err := planner.CompilePrimaryQuery(context.Background(), pollingTestSource(string(config)))
		if interval == 30 || interval == 90 {
			var failure *QueryPlanCompileError
			if !errors.As(err, &failure) || failure.Reason != "QUERY_FTA_INTERVAL_INVALID" {
				t.Fatalf("interval=%d error=%v", interval, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		wantStep, wantWindow := int64(120000), "120s"
		if interval == 0 {
			wantStep, wantWindow = 60000, "60s"
		}
		if facts.StepMillis != wantStep || facts.QueryList[0].TimeAggregation.Window != wantWindow {
			t.Fatalf("interval=%d facts=%+v", interval, facts)
		}
	}
}

func TestLogSearchQueryString(t *testing.T) {
	for raw, want := range map[string]string{"timeout": "*timeout*", "   ": "*", "a &amp;&amp; b": "a && b", "field:value": "field:value", "foo AND bar": "foo AND bar"} {
		if got := logSearchQueryString(raw); got != want {
			t.Errorf("%q => %q want %q", raw, got, want)
		}
	}
	planner, _ := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{})
	source := pollingTestSource(`{"data_source_label":"bk_log_search","data_type_label":"time_series","index_set_id":7,"result_table_id":"unused","agg_interval":60,"agg_condition":[{"key":"__dist_01","method":"eq","value":["a"]}]}`)
	facts, err := planner.CompilePrimaryQuery(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if facts.QueryList[0].TableID != "bklog_index_set_7_clustered" {
		t.Fatalf("query=%+v", facts.QueryList[0])
	}
}
