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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// buildHorizonCatalog builds the catalog of one strategy, from its stored
// document, with this no_data_config section and this platform horizon. The
// item's aggregation interval is 60 seconds and it sets no detect_interval,
// so the no-data trigger steps at 60 seconds.
func buildHorizonCatalog(t *testing.T, noDataSection string, platformHorizon int64) controlplane.Catalog {
	t.Helper()
	item := map[string]any{
		"id": 11, "query_md5": "m", "expression": "a", "name": "item",
		"query_configs": []any{map[string]any{
			"data_source_label": "bk_monitor", "data_type_label": "time_series",
			"result_table_id": "system.cpu", "metric_field": "usage", "agg_method": "AVG",
			"agg_interval": 60, "alias": "a", "agg_dimension": []any{"host"},
		}},
		"algorithms":     []any{map[string]any{"level": 1, "type": "Threshold", "config": []any{map[string]any{"method": "gte", "threshold": 1}}}},
		"no_data_config": json.RawMessage(noDataSection),
	}
	document := map[string]any{
		"id": 1001, "bk_biz_id": 2, "update_time": 1700000000, "name": "s", "scenario": "os",
		"detects": []any{map[string]any{"level": 1, "trigger_config": map[string]any{"check_window": 1, "count": 1}}},
		"items":   []any{item},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{{SourceID: "1001", Document: encoded,
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}},
		Planner:      planner,
		NoDataPolicy: controlplane.NoDataPolicy{TrackingHorizonSeconds: platformHorizon},
	})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

// horizonWarning is the strategy's NO_DATA_TRIGGER_BEYOND_HORIZON note, and
// whether it has one.
func horizonWarning(catalog controlplane.Catalog) (controlplane.ObjectDisposition, bool) {
	for _, disposition := range catalog.Dispositions {
		if disposition.Reason == controlplane.ReasonNoDataTriggerBeyondHorizon {
			return disposition, true
		}
	}
	return controlplane.ObjectDisposition{}, false
}

// A no-data trigger the tracking horizon stops before it can fire is warned
// about, with the numbers, and nothing else about the strategy changes.
//
// The line comes from the contract, not from this build: tracking stops on
// the round where the absence is H old, with no verdict (retention proposal,
// section 1 item 2), and continuous=N needs N absent points one period apart
// (P9), the first of them at the absence's start, so the earliest alert is
// (N-1) periods in. With N=11 and a 60-second period that is 600 seconds:
// a horizon of 601 seconds leaves the alert one second to spare, and 600 or
// less never lets it through.
//
// On every side the Plan runs: accepted, its no-data configured and not
// suspended, counted under its roster source like any other.
func TestANoDataTriggerTheHorizonStopsFirstIsWarnedAndNothingElseChanges(t *testing.T) {
	const section = `{"is_enabled":true,"continuous":11}`
	for name, test := range map[string]struct {
		section string
		horizon int64
		warned  bool
		detail  string
	}{
		"one second to spare": {section: section, horizon: 601},
		"exactly the horizon": {section: section, horizon: 600, warned: true,
			detail: "continuous=11 period=60 earliest_alert_after=600 tracking_horizon=600 horizon_source=PLATFORM"},
		"past the horizon": {section: section, horizon: 599, warned: true,
			detail: "continuous=11 period=60 earliest_alert_after=600 tracking_horizon=599 horizon_source=PLATFORM"},
		"the item's own horizon": {section: `{"is_enabled":true,"continuous":11,"tracking_horizon_seconds":600}`, warned: true,
			detail: "continuous=11 period=60 earliest_alert_after=600 tracking_horizon=600 horizon_source=STRATEGY"},
		"no horizon configured anywhere": {section: section, horizon: 0},
	} {
		t.Run(name, func(t *testing.T) {
			catalog := buildHorizonCatalog(t, test.section, test.horizon)
			warning, warned := horizonWarning(catalog)
			if warned != test.warned {
				t.Fatalf("warned = %t (%+v), want %t", warned, warning, test.warned)
			}
			if warned {
				if warning.Disposition != controlplane.DispositionConfigNoted || warning.Scope != "PLAN" ||
					warning.Detail != test.detail || warning.FieldPath != "items[0].no_data_config.continuous" {
					t.Fatalf("warning = %+v, want CONFIG_NOTED on the Plan with detail %q", warning, test.detail)
				}
			}
			for _, disposition := range catalog.Dispositions {
				if disposition.Disposition != controlplane.DispositionAccepted &&
					disposition.Disposition != controlplane.DispositionConfigNoted {
					t.Fatalf("the strategy is filed as %+v; the warning must not withhold anything", disposition)
				}
			}
			plan := onlyPlan(t, catalog)
			if plan.Plan.NoData == nil || plan.NoDataSuspended != "" {
				t.Fatalf("no_data=%+v suspended=%q, want no-data configured and running", plan.Plan.NoData, plan.NoDataSuspended)
			}
			composition := controlplane.ComposeCatalog(catalog)
			total := composition.NoDataPlansUnclassified
			for _, count := range composition.NoDataPlans {
				total += count
			}
			if total != 1 || len(composition.SuspendedNoDataObjects) != 0 {
				t.Fatalf("no-data Plans counted %d, suspended %+v; want the one Plan counted as detecting no-data",
					total, composition.SuspendedNoDataObjects)
			}
		})
	}
}
