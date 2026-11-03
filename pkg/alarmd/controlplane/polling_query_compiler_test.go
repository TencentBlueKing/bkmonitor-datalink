package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

// A filter with no matcher in it compiles to no match. "{}" is not a
// selector: the provider refused the query beside it on every round, and a
// strategy saved with an empty filter detected nothing.
func TestPollingPromQLFilterWithoutAMatcherIsNoMatch(t *testing.T) {
	planner, _ := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{})
	for _, filter := range []string{`null`, `{}`, `{"labels":{}}`, `{"ignored":1}`, `{"labels":{"nested":{}},"count":2}`} {
		t.Run(filter, func(t *testing.T) {
			source := pollingTestSource(`{"data_source_label":"prometheus","data_type_label":"time_series","promql":"avg_over_time(up[60s])","agg_interval":60,"filter_dict":` + filter + `}`)
			facts, err := planner.CompilePrimaryQuery(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			if facts.PromQL == nil || facts.PromQL.Match != "" {
				t.Fatalf("promql=%+v, want no match", facts.PromQL)
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

// FTA event sources are refused by name, whatever their configuration: the
// FTA query is not supported, and nothing in the compiler's facts turns it on.
func TestAnFTASourceIsRefusedAsUnsupported(t *testing.T) {
	planner, err := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, config := range []string{
		`{"data_source_label":"bk_fta","data_type_label":"event","alert_name":"__EVENT_PLUGIN__zabbix","alias":"a","agg_interval":60,"agg_dimension":["tags.env"],"agg_condition":[{"key":"tags.env","method":"include","value":["prod"]}]}`,
		`{"data_source_label":"bk_fta","data_type_label":"event","alert_name":"CPUHigh","agg_interval":120}`,
		`{"data_source_label":"bk_fta","data_type_label":"event","agg_interval":30}`,
	} {
		_, err := planner.CompilePrimaryQuery(context.Background(), pollingTestSource(config))
		var failure *QueryPlanCompileError
		if !errors.As(err, &failure) || failure.Disposition != DispositionUnsupported || failure.Reason != "QUERY_FTA_UNSUPPORTED" {
			t.Fatalf("%s: %v, want UNSUPPORTED_PHASE2_CAPABILITY QUERY_FTA_UNSUPPORTED", config, err)
		}
	}
}

func TestPollingBKDataLocalTimeBoundary(t *testing.T) {
	planner, _ := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{})
	source := pollingTestSource(`{"data_source_label":"bk_data","data_type_label":"time_series","result_table_id":"2_metric","metric_field":"value","alias":"a","agg_method":"AVG","agg_interval":60}`)
	for _, functions := range []string{`[]`, `[{"id":"abs","params":[]}]`} {
		var query map[string]json.RawMessage
		if err := json.Unmarshal(source.QueryConfigs[0], &query); err != nil {
			t.Fatal(err)
		}
		query["functions"] = json.RawMessage(functions)
		source.QueryConfigs[0], _ = json.Marshal(query)
		_, err := planner.CompilePrimaryQuery(context.Background(), source)
		var failure *QueryPlanCompileError
		if !errors.As(err, &failure) || failure.Disposition != DispositionUnsupported || failure.Reason != "QUERY_BK_DATA_LOCAL_TIME_NOT_MIGRATED" {
			t.Fatalf("functions=%s localTime boundary=%v", functions, err)
		}
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

// The event name and the recovery filter go into every OR group of a custom
// event strategy's conditions, as the platform's _filter_dict_to_conditions
// puts its filters, not once after the last group: appended at the end they
// filtered only that group, and every other group counted every event of the
// table, recoveries included.
func TestCustomEventFiltersAreAndedIntoEveryOrGroup(t *testing.T) {
	planner, _ := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{})
	facts, err := planner.CompilePrimaryQuery(context.Background(), pollingTestSource(`{"data_source_label":"custom","data_type_label":"event",
		"result_table_id":"base_tenant_2_event","custom_event_name":"AgentLost","agg_method":"COUNT","agg_interval":60,
		"agg_dimension":["bk_target_ip","bk_target_cloud_id","bk_agent_id"],
		"agg_condition":[{"key":"bk_target_ip","method":"eq","value":["192.0.2.10"]},
			{"condition":"or","key":"bk_target_ip","method":"eq","value":["192.0.2.11"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	conditions := facts.QueryList[0].Conditions
	want := []string{"event_name", "dimensions.event_type", "dimensions.bk_target_ip", "event_name", "dimensions.event_type", "dimensions.bk_target_ip"}
	wantConnectors := []string{"and", "and", "or", "and", "and"}
	if len(conditions.Fields) != len(want) || len(conditions.Connectors) != len(wantConnectors) {
		t.Fatalf("conditions %+v, want both OR groups filtered", conditions)
	}
	for index, field := range want {
		if conditions.Fields[index].Field != field {
			t.Fatalf("condition %d is %q, want %q: %+v", index, conditions.Fields[index].Field, field, conditions)
		}
	}
	for index, connector := range wantConnectors {
		if conditions.Connectors[index] != connector {
			t.Fatalf("connector %d is %q, want %q: %+v", index, conditions.Connectors[index], connector, conditions)
		}
	}
}

// A dimension or condition key that already names a dimensions field keeps
// its name, as the platform adds the prefix only to one that does not start
// with it (is_dimensions_field).
func TestAnEventFieldAlreadyUnderDimensionsIsNotPrefixedTwice(t *testing.T) {
	planner, _ := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{})
	facts, err := planner.CompilePrimaryQuery(context.Background(), pollingTestSource(`{"data_source_label":"custom","data_type_label":"event",
		"result_table_id":"k8s_event","custom_event_name":"PodError","agg_interval":60,"agg_dimension":["dimensions.namespace"],
		"agg_condition":[{"key":"dimensions.pod","method":"eq","value":["api-0"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	clause := facts.QueryList[0]
	if clause.Dimensions[0] != "dimensions.namespace" {
		t.Fatalf("dimension %q, want dimensions.namespace", clause.Dimensions[0])
	}
	for _, field := range clause.Conditions.Fields {
		if field.Field == "dimensions.dimensions.pod" {
			t.Fatalf("condition key prefixed twice: %+v", clause.Conditions)
		}
	}
}

// Several PromQL queries without expression_mode are the retired form, named
// so with the three ways to rewrite it; PromQL beside another source is not
// that form and keeps its own word.
func TestTheRetiredMultiPromQLFormIsRefusedByName(t *testing.T) {
	planner, _ := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{})
	promql := func(alias string) string {
		return `{"data_source_label":"prometheus","data_type_label":"time_series","alias":"` + alias + `","promql":"sum by (host) (up)","agg_interval":60}`
	}
	retired := pollingTestSource(promql("a"))
	retired.QueryConfigs = append(retired.QueryConfigs, json.RawMessage(promql("b")))
	_, err := planner.CompilePrimaryQuery(context.Background(), retired)
	var failure *QueryPlanCompileError
	if !errors.As(err, &failure) || failure.Reason != "QUERY_PROMQL_FORM_RETIRED" || failure.Err == nil ||
		!strings.Contains(failure.Err.Error(), "expression_mode multi-PromQL expression") || !strings.Contains(failure.Err.Error(), "separate strategies") {
		t.Fatalf("err = %v, want QUERY_PROMQL_FORM_RETIRED naming the rewrites", err)
	}
	// The same queries marked expression_mode are the platform's expression,
	// which runs as one PromQL: not the retired form, and compiled as one.
	marked := func(alias string) string {
		return strings.Replace(promql(alias), `"alias"`, `"expression_mode":"promql","alias"`, 1)
	}
	expression := pollingTestSource(marked("a"))
	expression.QueryConfigs = append(expression.QueryConfigs, json.RawMessage(marked("b")))
	if facts, err := planner.CompilePrimaryQuery(context.Background(), expression); err != nil || facts.PromQL == nil {
		t.Fatalf("facts=%+v err=%v, want the marked expression compiled as one PromQL", facts.PromQL, err)
	}
	mixed := pollingTestSource(promql("a"))
	mixed.QueryConfigs = append(mixed.QueryConfigs, json.RawMessage(`{"data_source_label":"bk_monitor","data_type_label":"time_series",`+
		`"metric_field":"usage","alias":"b","agg_method":"AVG","agg_interval":60,"result_table_id":"system.cpu_summary"}`))
	if _, err := planner.CompilePrimaryQuery(context.Background(), mixed); !errors.As(err, &failure) || failure.Reason != "QUERY_MIXED_PROMQL_NOT_MIGRATED" {
		t.Fatalf("err = %v, want QUERY_MIXED_PROMQL_NOT_MIGRATED for PromQL beside another source", err)
	}
}

// A group whose conditions all have no value still counts the event, as the
// platform's query does: the filters go before each group, so the group's
// "or" stays on them when its own condition is dropped for having no value.
func TestAnOrGroupOfOnlyEmptyConditionsStillFiltersByTheEvent(t *testing.T) {
	planner, _ := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{})
	facts, err := planner.CompilePrimaryQuery(context.Background(), pollingTestSource(`{"data_source_label":"custom","data_type_label":"event",
		"result_table_id":"base_tenant_2_event","custom_event_name":"AgentLost","agg_method":"COUNT","agg_interval":60,
		"agg_dimension":["bk_target_ip"],
		"agg_condition":[{"key":"bk_target_ip","method":"eq","value":["192.0.2.10"]},
			{"condition":"or","key":"bk_target_ip","method":"eq","value":[]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	conditions := facts.QueryList[0].Conditions
	want := []string{"event_name", "dimensions.event_type", "dimensions.bk_target_ip", "event_name", "dimensions.event_type"}
	wantConnectors := []string{"and", "and", "or", "and"}
	if len(conditions.Fields) != len(want) || len(conditions.Connectors) != len(wantConnectors) {
		t.Fatalf("conditions %+v, want the empty group kept as the filters alone", conditions)
	}
	for index, field := range want {
		if conditions.Fields[index].Field != field {
			t.Fatalf("condition %d is %q, want %q: %+v", index, conditions.Fields[index].Field, field, conditions)
		}
	}
	for index, connector := range wantConnectors {
		if conditions.Connectors[index] != connector {
			t.Fatalf("connector %d is %q, want %q: %+v", index, conditions.Connectors[index], connector, conditions)
		}
	}
}
