// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// A Plan's signal type follows its query configs.
//
// The published event carries it so the consumer can route on what the
// operator is watching, and nothing downstream can work it out again: a record
// from a log count and a record from a metric are the same shape by the time
// they reach the sink. So the only place this can be wrong is here, and the
// only way to see it is to compile each kind and read what came out -- a test
// that compiles one kind and asserts "metric" passes for a build that answers
// metric to everything.
func TestAPlanSignalTypeFollowsItsQueryConfigs(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		sourceKind string
		dataType   string
		want       string
	}{
		{"a metric", "bk_monitor", "time_series", contract.SignalTypeMetric},
		{"a log", "bk_monitor", "log", contract.SignalTypeLog},
		{"an event", "custom", "event", contract.SignalTypeEvent},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := signalTypeStrategyDocument(t, test.sourceKind, test.dataType)
			catalog, buildErr := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
				Strategies: []controlplane.SourceStrategy{{
					SourceID: "301", Document: document,
					Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
				}},
				Planner: planner,
			})
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			if len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != 1 {
				t.Fatalf("catalog = %+v; dispositions = %+v", catalog.QueryGroups, catalog.Dispositions)
			}
			plan := catalog.QueryGroups[0].Plans[0]
			if plan.Plan.SignalType != test.want {
				t.Fatalf("signal type = %q, want %q", plan.Plan.SignalType, test.want)
			}
			// And it survives the split the Catalog is published through: it
			// is read only when an event is rendered, so it travels in the
			// output context, and a field dropped there is dropped silently.
			if got := controlplane.BuildOutputContext(plan).SignalType; got != test.want {
				t.Fatalf("output context signal type = %q, want %q", got, test.want)
			}
		})
	}
}

// An item whose configs disagree carries no signal type rather than one of
// them. Which input the operator meant is not stated anywhere, and both
// tie-breaks -- first wins, or one kind wins -- would be this build deciding
// it.
func TestAnItemWhoseConfigsDisagreeCarriesNoSignalType(t *testing.T) {
	if got := contract.SignalTypeForDataTypes([]string{"time_series", "log"}); got != "" {
		t.Fatalf("signal type = %q, want none for configs that disagree", got)
	}
	if got := contract.SignalTypeForDataTypes([]string{"time_series", "time_series"}); got != contract.SignalTypeMetric {
		t.Fatalf("signal type = %q, want metric for configs that agree", got)
	}
	// One config this build cannot name makes the whole item unnamed, rather
	// than letting the rest agree on a type the item may not have.
	if got := contract.SignalTypeForDataTypes([]string{"time_series", ""}); got != "" {
		t.Fatalf("signal type = %q, want none when one config is unknown", got)
	}
}

// Every source a query can be compiled from has a signal type.
//
// A data type with no mapping answers "" and the event then omits the field --
// which is the right behaviour for a value this build cannot name, and the
// wrong one to discover in production. The supported list is where a new
// source arrives, so the check is a scan of that list rather than a second
// list written here: the two would agree until somebody added to one.
func TestEverySupportedSourceHasASignalType(t *testing.T) {
	for _, semantics := range controlplane.SupportedSourceSemantics {
		parts := strings.SplitN(semantics, "/", 2)
		if len(parts) != 2 {
			t.Fatalf("source semantics %q is not source/type", semantics)
		}
		if signal := contract.SignalTypeForDataType(parts[1]); signal == "" {
			t.Fatalf("source %q compiles but its data type %q has no signal type, so every event from it "+
				"reaches the consumer with the field missing", semantics, parts[1])
		}
	}
}

func signalTypeStrategyDocument(t *testing.T, sourceLabel, dataType string) json.RawMessage {
	t.Helper()
	document, err := json.Marshal(map[string]any{
		"id": 301, "bk_biz_id": 2, "update_time": 1,
		"items": []any{map[string]any{
			"id": 1, "query_md5": "query-md5", "expression": "a", "unit": "",
			"query_configs": []any{map[string]any{
				"data_source_label": sourceLabel, "data_type_label": dataType, "metric_field": "usage",
				"alias": "a", "agg_dimension": []string{"host"}, "agg_method": "MAX", "agg_interval": 60,
				"result_table_id": "system.cpu",
			}},
			"algorithms": []any{map[string]any{
				"level": 1, "type": strategy.DetectorKindThreshold,
				"config": []any{map[string]any{"method": "gte", "threshold": 90}},
			}},
		}},
		"detects": []any{map[string]any{
			"level": 1, "priority": 1, "connector": "and",
			"trigger_config": map[string]any{"count": 1, "check_window": 1},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return document
}
