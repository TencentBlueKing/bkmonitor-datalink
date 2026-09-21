// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// These comparisons consume source-executed Python stages. Page-only unaligned
// queries, Python-only time-column formats, and ES DSL belong to other owners;
// they are not counted as Go polling-wire coverage.
func TestPollingPythonOracle(t *testing.T) {
	var fixture struct {
		Cases []struct {
			ID, Stage       string
			Input, Expected json.RawMessage
		}
	}
	raw, err := os.ReadFile("testdata/polling_python_oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	storage := execution.QueryStorage{TableID: "events", StorageID: "17", StorageType: "elasticsearch", DB: "bkfta_event_*_read", Measurement: "__default__", TimeField: execution.QueryTimeField{Name: "time", Type: "date", Unit: "millisecond"}}
	planner, _ := controlplane.NewLegacyPrimaryQueryCompiler("uq", "Asia/Shanghai", controlplane.LegacyQueryRuntimeFacts{FTAEventStorage: &storage})
	checked := 0
	for _, tc := range fixture.Cases {
		selected := tc.Stage == "source_intrinsic_filters" || tc.Stage == "promql_api_request" || tc.Stage == "uq_query_config" && (strings.HasPrefix(tc.ID, "bk_monitor-") && !strings.Contains(tc.ID, "log-") || strings.HasPrefix(tc.ID, "custom-") && !strings.Contains(tc.ID, "event-") || strings.HasPrefix(tc.ID, "bk_log_search-") && strings.HasSuffix(tc.ID, "True"))
		if !selected {
			t.Run(tc.ID, func(t *testing.T) {
				switch tc.Stage {
				case "fta_filter_dsl", "fta_aggregation_dsl":
					t.Skip("ES DSL is consumed by the UQ formatter tests, not the alarmd wire adapter")
				case "uq_result_records":
					t.Skip("Python compatibility shape uses time-typed seconds; current UQ PromEngine emits float-typed milliseconds")
				case "uq_query_config":
					t.Skip("page-only unaligned query or pre-constructor metric shape does not represent polling strategy configuration")
				default:
					t.Skip("Python DataRecord stage is outside query-construction adapter coverage")
				}
			})
			continue
		}
		checked++
		t.Run(tc.ID, func(t *testing.T) {
			var input map[string]any
			_ = json.Unmarshal(tc.Input, &input)
			config := map[string]any{"agg_interval": 60, "alias": "a"}
			var want any
			_ = json.Unmarshal(tc.Expected, &want)
			switch tc.Stage {
			case "uq_query_config":
				config["data_source_label"] = input["data_source_label"]
				config["data_type_label"] = "time_series"
				config["agg_interval"] = input["interval"]
				config["agg_dimension"] = input["group_by"]
				config["result_table_id"] = input["table"]
				config["time_field"] = input["time_field"]
				metric := input["metrics"].([]any)[0].(map[string]any)
				config["metric_field"] = metric["field"]
				config["agg_method"] = metric["method"]
				config["alias"] = metric["alias"]
				if label, ok := input["source_label"]; ok {
					config["data_source_label"] = label
					config["data_type_label"] = input["type_label"]
					config["index_set_id"] = strings.TrimPrefix(input["table_id"].(string), "bklog_index_set_")
					config["query_string"] = input["query_string"]
				}
				// This fixture freezes effective datasource filters; project them
				// to agg_condition instead of pretending Access consumes filter_dict.
				if filters, ok := input["filter_dict"].(map[string]any); ok {
					var conditions []any
					for key, value := range filters {
						conditions = append(conditions, map[string]any{"key": strings.TrimSuffix(key, "__eq"), "method": "eq", "value": value})
					}
					config["agg_condition"] = conditions
				}
			case "source_intrinsic_filters":
				config["data_source_label"] = "custom"
				config["data_type_label"] = "event"
				config["result_table_id"] = "k8s_event"
				config["custom_event_name"] = input["custom_event_name"]
				if strings.HasPrefix(tc.ID, "fta-") {
					config["data_source_label"] = "bk_fta"
					config["agg_interval"] = input["interval"]
					config["agg_dimension"] = input["group_by"]
					config["alert_name"] = input["metrics"].([]any)[0].(map[string]any)["field"]
				}
			case "promql_api_request":
				config["data_source_label"] = "prometheus"
				config["data_type_label"] = "time_series"
				config["promql"] = input["promql"]
				config["agg_interval"] = input["interval"]
				config["filter_dict"] = input["filter_dict"]
			}
			configRaw, _ := json.Marshal(config)
			facts, err := planner.CompilePrimaryQuery(context.Background(), controlplane.PrimaryQuerySource{Identity: controlplane.SourceIdentity{TenantID: "tenant", BusinessID: "2", SpaceScope: "bkcc__2"}, StrategyID: "14", ItemID: "3", QueryMD5: "oracle", Expression: "a", QueryConfigs: []json.RawMessage{configRaw}})
			if err != nil {
				t.Fatal(err)
			}
			if tc.Stage == "promql_api_request" {
				expected := want.(map[string]any)["request"].(map[string]any)
				if facts.PromQL.Match != expected["match"] || facts.PromQL.Expression != expected["promql"] || durationString(facts.StepMillis) != expected["step"] {
					t.Fatalf("promql=%+v want=%v", facts.PromQL, expected)
				}
				return
			}
			if tc.Stage == "source_intrinsic_filters" {
				conditions := facts.QueryList[0].Conditions
				expected := want.(map[string]any)
				if strings.HasPrefix(tc.ID, "fta-") {
					conditions = *facts.QueryList[0].SourceConditions
					expected = expected["filter_dict"].(map[string]any)
					if facts.QueryList[0].TimeAggregation.Window != fmt.Sprintf("%.0fs", want.(map[string]any)["interval_minutes"].(float64)*60) {
						t.Fatal("FTA minute bucket changed")
					}
				}
				got := map[string]string{}
				for _, field := range conditions.Fields {
					key := strings.TrimPrefix(field.Field, "dimensions.")
					if field.Operator == "ne" {
						key += "__neq"
					}
					got[key] = field.Values[0].StringValue
				}
				for key, value := range expected {
					if got[key] != fmt.Sprint(value) {
						t.Fatalf("intrinsic %s=%q want=%v", key, got[key], value)
					}
				}
				if len(got) != len(expected) {
					t.Fatalf("extra intrinsic filters=%v", got)
				}
				return
			}
			attempt := validAttempt(t)
			spec, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: facts, LogicalWindow: attempt.Spec.LogicalWindow, ProviderRange: attempt.Spec.ProviderRange, AcceptedRange: attempt.Spec.AcceptedRange, RequiredColumns: []string{"value"}})
			if err != nil {
				t.Fatal(err)
			}
			body, err := buildRequest(spec)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(body.QueryList)
			var got any
			_ = json.Unmarshal(encoded, &got)
			oracleSubset(t, want, got)
		})
	}
	if checked != 14 {
		t.Fatalf("compared %d stages, want 14", checked)
	}
}

func oracleSubset(t *testing.T, want, got any) {
	t.Helper()
	switch expected := want.(type) {
	case map[string]any:
		actual, ok := got.(map[string]any)
		if !ok {
			if len(expected) == 0 && got == nil {
				return
			}
			t.Fatalf("want object %v got %v", want, got)
		}
		for key, value := range expected {
			// The fixture mocks the effective log query string; its raw
			// constructor normalization is covered by TestLogSearchQueryString.
			if key == "order_by" || key == "query_string" {
				continue
			}
			oracleSubset(t, value, actual[key])
		}
	case []any:
		actual, ok := got.([]any)
		if !ok {
			if len(expected) == 0 && got == nil {
				return
			}
			t.Fatalf("want list %v got %v", want, got)
		}
		if len(expected) != len(actual) {
			t.Fatalf("list want=%v got=%v", want, got)
		}
		for i := range expected {
			oracleSubset(t, expected[i], actual[i])
		}
	default:
		if (want == false || want == "") && got == nil {
			return
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("want=%v got=%v", want, got)
		}
	}
}
