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
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// A count is read as a count whatever algorithm its Level runs. An ungrouped
// log count and an event count carry the same words on their primary - the
// zero is no observation, a log count's groups are not zero-filled, an event
// count's range reaches back for its quiet groups - under a threshold, whose
// primary the control plane builds itself, and under the algorithms whose
// primary is bound from a frozen template: a ring ratio, an advanced ring
// ratio, a year-round range. A metric time series carries none of them under
// any.
func TestEveryAlgorithmFamilysCountPrimaryCarriesTheCountWords(t *testing.T) {
	queries := map[string]map[string]any{
		"ungrouped log count": {"data_source_label": "bk_log_search", "data_type_label": "log", "index_set_id": 7,
			"agg_method": "COUNT", "alias": "a", "agg_interval": 60, "agg_dimension": []any{}},
		"event count": {"data_source_label": "custom", "data_type_label": "event", "result_table_id": "k8s_event",
			"custom_event_name": "PodError", "alias": "a", "agg_interval": 60, "agg_dimension": []any{"namespace"}},
		"metric": {"data_source_label": "bk_monitor", "data_type_label": "time_series", "result_table_id": "system.cpu",
			"metric_field": "usage", "agg_method": "AVG", "alias": "a", "agg_interval": 60, "agg_dimension": []any{"host"}},
	}
	families := map[string]any{
		strategy.DetectorKindThreshold:         []any{map[string]any{"method": "gte", "threshold": 1}},
		strategy.DetectorKindSimpleRingRatio:   map[string]any{"floor": 50, "ceil": nil},
		strategy.DetectorKindAdvancedRingRatio: map[string]any{"ceil": 20, "ceil_interval": 3, "fetch_type": "last"},
		strategy.DetectorKindYearRoundRange:    map[string]any{"days": 2, "method": "gte", "ratio": 2, "shock": 0},
	}
	for name, query := range queries {
		for kind, config := range families {
			t.Run(name+"/"+kind, func(t *testing.T) {
				source := g4SourceStrategy(t, 901, kind, config)
				var document map[string]any
				if err := json.Unmarshal(source.Document, &document); err != nil {
					t.Fatal(err)
				}
				document["items"].([]any)[0].(map[string]any)["query_configs"] = []any{query}
				raw, err := json.Marshal(document)
				if err != nil {
					t.Fatal(err)
				}
				source.Document = raw
				fact, err := freezeG4Catalog(t, buildG4Catalog(t, source))
				if err != nil {
					t.Fatal(err)
				}
				var primary *execution.DataRequirement
				for index := range fact.Requirements {
					if fact.Requirements[index].Role == execution.InputRolePrimary {
						primary = &fact.Requirements[index]
					}
				}
				if primary == nil {
					t.Fatalf("no primary requirement in %+v", fact.Requirements)
				}
				wantZero, wantUnfilled, wantLead := name != "metric", name == "ungrouped log count", name == "event count"
				if primary.ZeroIsNoObservation != wantZero || primary.UnfilledCounts != wantUnfilled || (primary.ProviderLeadSeconds > 0) != wantLead {
					t.Fatalf("primary zero=%t unfilled=%t lead=%d, want zero=%t unfilled=%t lead>0=%t",
						primary.ZeroIsNoObservation, primary.UnfilledCounts, primary.ProviderLeadSeconds, wantZero, wantUnfilled, wantLead)
				}
			})
		}
	}
}

// An event count's range reaches back as far as its Levels read: the
// trigger and recovery windows, and for a Level written to end on no data
// the (R+T+1) windows its end waits for, whichever is longer. Pinned by
// value under the threshold, whose primary the control plane builds, and
// under a ring ratio, whose primary is bound from a frozen template: with
// T = 1 and R = 2 at a minute, -nodata reaches 4 minutes back, more than
// either family's own history, and without it the range is shorter.
func TestAnEventCountWrittenToEndOnNoDataReachesBackItsWholeWait(t *testing.T) {
	families := map[string]any{
		strategy.DetectorKindThreshold:       []any{map[string]any{"method": "gte", "threshold": 1}},
		strategy.DetectorKindSimpleRingRatio: map[string]any{"floor": 50, "ceil": nil},
	}
	lead := func(t *testing.T, kind string, config any, setter string) int64 {
		t.Helper()
		source := g4SourceStrategy(t, 902, kind, config)
		var document map[string]any
		if err := json.Unmarshal(source.Document, &document); err != nil {
			t.Fatal(err)
		}
		document["items"].([]any)[0].(map[string]any)["query_configs"] = []any{map[string]any{
			"data_source_label": "custom", "data_type_label": "event", "result_table_id": "k8s_event",
			"custom_event_name": "PodError", "alias": "a", "agg_interval": 60, "agg_dimension": []any{"namespace"}}}
		for _, detect := range document["detects"].([]any) {
			detect.(map[string]any)["trigger_config"] = map[string]any{"count": 1, "check_window": 1}
			detect.(map[string]any)["recovery_config"] = map[string]any{"check_window": 2, "status_setter": setter}
		}
		raw, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		source.Document = raw
		fact, err := freezeG4Catalog(t, buildG4Catalog(t, source))
		if err != nil {
			t.Fatal(err)
		}
		for _, requirement := range fact.Requirements {
			if requirement.Role == execution.InputRolePrimary {
				return requirement.ProviderLeadSeconds
			}
		}
		t.Fatalf("no primary requirement in %+v", fact.Requirements)
		return 0
	}
	for kind, config := range families {
		t.Run(kind, func(t *testing.T) {
			if got := lead(t, kind, config, "recovery-nodata"); got != 4*60 {
				t.Fatalf("-nodata lead = %d s, want (R+T+1) = 4 windows of 60 s", got)
			}
			if got := lead(t, kind, config, "recovery"); got <= 0 || got >= 4*60 {
				t.Fatalf("lead without -nodata = %d s, want the Level's own history, under 4 windows", got)
			}
		})
	}
}
