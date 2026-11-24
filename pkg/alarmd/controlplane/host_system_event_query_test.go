// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

// hostSystemEvents are the host system events a built-in strategy reads as
// custom events, each with the dimensions the platform's migration gives it
// (strategy_migration.SYSTEM_EVENT_TARGET_CONFIGS). OOM leaves the
// platform's constraint out: every OOM event carries CONSTRAINT_MEMCG there,
// so it would split no alert.
var hostSystemEvents = []struct {
	name       string
	dimensions []string
}{
	{"AgentLost", []string{"bk_target_ip", "bk_target_cloud_id", "bk_agent_id"}},
	{"DiskReadonly", []string{"bk_target_ip", "bk_target_cloud_id", "position", "type", "fs"}},
	{"DiskFull", []string{"bk_target_ip", "bk_target_cloud_id", "disk", "fstype", "file_system"}},
	{"CoreFile", []string{"bk_target_ip", "bk_target_cloud_id", "executable_path", "executable", "signal"}},
	{"OOM", []string{"bk_target_ip", "bk_target_cloud_id", "process"}},
}

// A host system event strategy names the system_event data label, which the
// platform hands the query service as written, as it does every built-in
// event table (CustomEventDataSource.INNER_TABLE); any other table is read
// through its default partition (BkMonitorLogDataSource._get_unify_query_table).
// Either way the query counts the strategy's own event, recoveries left out,
// by its dimensions under the dimensions object, and the series come back
// keyed by the bare dimension, as the platform strips the prefix from what
// the query service answers.
func TestAHostSystemEventReadsTheBuiltInEventTableAsWritten(t *testing.T) {
	planner, err := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range hostSystemEvents {
		for table, want := range map[string]string{
			"system_event":        "system_event",
			"base_system_2_event": "base_system_2_event.__default__",
		} {
			t.Run(event.name+"/"+table, func(t *testing.T) {
				config, err := json.Marshal(map[string]any{"data_source_label": "custom", "data_type_label": "event",
					"result_table_id": table, "custom_event_name": event.name, "agg_method": "COUNT", "agg_interval": 60,
					"agg_dimension": event.dimensions})
				if err != nil {
					t.Fatal(err)
				}
				facts, err := planner.CompilePrimaryQuery(context.Background(), pollingTestSource(string(config)))
				if err != nil {
					t.Fatal(err)
				}
				if len(facts.QueryList) != 1 {
					t.Fatalf("compiled %d queries, want one", len(facts.QueryList))
				}
				clause := facts.QueryList[0]
				if clause.TableID != want || clause.DataSource != "bkapm" || clause.FieldName != "_index" ||
					clause.TimeField != "time" || clause.TimeAggregation.Method != "count_over_time" {
					t.Fatalf("query %+v, want documents of %s counted on bkapm by time", clause, want)
				}

				bare := append([]string{}, event.dimensions...)
				sort.Strings(bare)
				prefixed := make([]string, 0, len(bare))
				for _, dimension := range bare {
					prefixed = append(prefixed, "dimensions."+dimension)
				}
				grouped := append([]string{}, clause.Dimensions...)
				sort.Strings(grouped)
				if !reflect.DeepEqual(grouped, prefixed) {
					t.Fatalf("grouped by %v, want %v", clause.Dimensions, prefixed)
				}

				conditions := clause.Conditions
				if len(conditions.Fields) != 2 || !reflect.DeepEqual(conditions.Connectors, []string{"and"}) {
					t.Fatalf("conditions %+v, want the event name and the recovery filter alone", conditions)
				}
				for index, wantField := range [][3]string{{"event_name", "eq", event.name}, {"dimensions.event_type", "ne", "recovery"}} {
					field := conditions.Fields[index]
					if field.Field != wantField[0] || field.Operator != wantField[1] || len(field.Values) != 1 ||
						field.Values[0].StringValue != wantField[2] {
						t.Fatalf("condition %d is %+v, want %s %s %s", index, field, wantField[0], wantField[1], wantField[2])
					}
				}

				normalization := facts.Normalization
				if !reflect.DeepEqual(normalization.DatasetContract.IdentityFields, bare) {
					t.Fatalf("identity %v, want the bare dimensions %v", normalization.DatasetContract.IdentityFields, bare)
				}
				for _, dimension := range bare {
					if normalization.DimensionAliases["dimensions."+dimension] != dimension {
						t.Fatalf("aliases %v do not key dimensions.%s back as %s", normalization.DimensionAliases, dimension, dimension)
					}
				}
			})
		}
	}
}

// A global strategy over custom events grouped by bk_biz_id reads only
// events that carry a business: its query asks for dimensions.bk_biz_id to
// exist and not be empty, so an event without one is left out of detection
// instead of being filed under the global business. On the system_event
// indices, ne "" alone keeps documents missing the field
// and existed alone keeps an empty value, so both are sent. An ordinary
// strategy grouped by bk_biz_id, a global one not grouped by it, and a
// global time series grouped by it are compiled without them.
func TestAGlobalHostSystemEventGroupedByBusinessReadsOnlyEventsCarryingOne(t *testing.T) {
	planner, err := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{})
	if err != nil {
		t.Fatal(err)
	}
	event := func(dimensions ...string) string {
		config, _ := json.Marshal(map[string]any{"data_source_label": "custom", "data_type_label": "event",
			"result_table_id": "system_event", "custom_event_name": "OOM", "agg_method": "COUNT", "agg_interval": 60,
			"agg_dimension": dimensions})
		return string(config)
	}
	timeSeries := `{"data_source_label":"bk_monitor","data_type_label":"time_series","result_table_id":"system.cpu_summary",` +
		`"metric_field":"usage","agg_method":"AVG","agg_interval":60,"agg_dimension":["bk_target_ip","bk_biz_id"]}`
	type field struct{ name, operator, value string }
	recovery := []field{{"event_name", "eq", "OOM"}, {"dimensions.event_type", "ne", "recovery"}}
	carrying := append(append([]field{}, recovery...), field{"dimensions.bk_biz_id", "existed", ""}, field{"dimensions.bk_biz_id", "ne", ""})
	for name, test := range map[string]struct {
		config string
		global bool
		want   []field
	}{
		"global, grouped by bk_biz_id":      {config: event("bk_target_ip", "bk_target_cloud_id", "process", "bk_biz_id"), global: true, want: carrying},
		"ordinary, grouped by bk_biz_id":    {config: event("bk_target_ip", "bk_target_cloud_id", "process", "bk_biz_id"), want: recovery},
		"global, not grouped by bk_biz_id":  {config: event("bk_target_ip", "bk_target_cloud_id", "process"), global: true, want: recovery},
		"global time series with bk_biz_id": {config: timeSeries, global: true},
	} {
		t.Run(name, func(t *testing.T) {
			source := pollingTestSource(test.config)
			source.Identity.GlobalBusiness = test.global
			facts, err := planner.CompilePrimaryQuery(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			if len(facts.QueryList) != 1 {
				t.Fatalf("compiled %d queries, want one", len(facts.QueryList))
			}
			var got []field
			for _, condition := range facts.QueryList[0].Conditions.Fields {
				value := ""
				if len(condition.Values) == 1 {
					value = condition.Values[0].StringValue
				}
				got = append(got, field{condition.Field, condition.Operator, value})
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("conditions %+v, want %+v", got, test.want)
			}
		})
	}
}
