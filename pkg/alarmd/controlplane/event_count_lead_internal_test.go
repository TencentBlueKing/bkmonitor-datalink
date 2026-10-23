// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Only a query that reads custom/event and nothing else asks the provider
// from earlier: a log count is a time series whose empty answer does not
// recover, and a query mixing sources is not an event count.
func TestOnlyAnEventCountQueryLeads(t *testing.T) {
	for semantics, want := range map[string]bool{
		"custom/event": true, "bk_log_search/log": false, "bk_monitor/log": false, "bk_monitor/time_series": false,
	} {
		query := execution.QueryPlanFacts{SourceSemantics: []string{semantics}, StepMillis: 60_000}
		if got := readsEventCountOnly(query); got != want {
			t.Errorf("%s: leads %v, want %v", semantics, got, want)
		}
	}
	mixed := execution.QueryPlanFacts{SourceSemantics: []string{"custom/event", "bk_monitor/time_series"}, StepMillis: 60_000}
	if readsEventCountOnly(mixed) || readsEventCountOnly(execution.QueryPlanFacts{StepMillis: 60_000}) {
		t.Fatal("a mixed query or one without source semantics leads")
	}
}

// The lead is, by window, the most history points of a Level due with it -
// N + R - 1 - times the window, so the range is N + R windows; Levels with
// another window lead for theirs; a lead that is not a whole number of steps
// is rounded up to one; no points lead nothing.
func TestTheLeadIsTheMostDemandingLevelsHistoryInWholeSteps(t *testing.T) {
	leads := providerLeads(60_000, []levelHistory{{window: 60, points: 4}, {window: 60, points: 9}, {window: 300, points: 2}, {window: 120, points: 0}})
	if leads[60] != 540 || leads[300] != 600 || leads[120] != 0 {
		t.Fatalf("leads = %v, want 540 for 60 s (9 points), 600 for 300 s, none for no points", leads)
	}
	if rounded := providerLeads(45_000, []levelHistory{{window: 60, points: 1}}); rounded[60] != 90 {
		t.Fatalf("a 60 s lead on a 45 s step = %d, want it rounded up to two steps, 90", rounded[60])
	}
}
