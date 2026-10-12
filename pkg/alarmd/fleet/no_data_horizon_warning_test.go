// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"net/http"
	"strings"
	"testing"
)

// The warning for a no-data trigger the tracking horizon stops first is read
// where an owner looks: strategy.get and the diagnosis. Both say the
// strategy is detecting, name the reason in the page's words, and carry the
// numbers the owner chooses between.
func TestANoDataTriggerBeyondTheHorizonIsReadOnTheStrategyAndItsDiagnosis(t *testing.T) {
	const detail = "continuous=11 period=60 earliest_alert_after=600 tracking_horizon=600 horizon_source=PLATFORM"
	facts := diagnosisFacts()
	facts["4110"] = StrategyLookupFacts{Available: true, Found: true, Publication: StrategyPublication{SnapshotRevision: "s1", Epoch: 7},
		Plans: plans(planA),
		Dispositions: []StrategyDisposition{
			{Scope: "PLAN", Disposition: "ACCEPTED"},
			{Scope: "PLAN", Disposition: "CONFIG_NOTED", Reason: "NO_DATA_TRIGGER_BEYOND_HORIZON",
				FieldPath: "items[0].no_data_config.continuous", Detail: detail}}}

	handler := standingHandler(t, func(id string) StrategyLookupFacts { return facts[id] }, nil)
	status, body := get(t, handler, "/api/strategies/4110")
	if status != http.StatusOK || body["standing"] != string(StandingDetecting) {
		t.Fatalf("status %d standing %v, want DETECTING: the warning withholds nothing", status, body["standing"])
	}
	line, _ := body["line"].(string)
	if !strings.Contains(line, "1 项配置有提示、仍在检测：PLAN：CONFIG_NOTED/NO_DATA_TRIGGER_BEYOND_HORIZON"+
		"（items[0].no_data_config.continuous）——无数据告警要连续缺席的时长达到或超过追踪期限") ||
		strings.Contains(line, "项被扣住") || strings.Contains(line, "全部被扣住") || strings.Contains(line, "部分生效") {
		t.Fatalf("line = %q, want the note in the page's words and nothing withheld", line)
	}
	if !strategyBodyCarriesDetail(body["dispositions"], detail) {
		t.Fatalf("dispositions = %v, want the warning with its numbers %q", body["dispositions"], detail)
	}

	rig := newDiagnosisRig(t, facts, nil)
	rig.universe = []string{"4110"}
	page := rig.page(t, "", 0)
	if len(page.Strategies) != 1 {
		t.Fatalf("diagnosis rows = %+v, want the one strategy", page.Strategies)
	}
	row := page.Strategies[0]
	if row.Verdict != StateDetecting {
		t.Fatalf("diagnosis verdict = %s, want %s", row.Verdict, StateDetecting)
	}
	found := false
	for _, disposition := range row.Dispositions {
		found = found || (disposition.Reason == "NO_DATA_TRIGGER_BEYOND_HORIZON" && disposition.Detail == detail)
	}
	if !found {
		t.Fatalf("diagnosis dispositions = %+v, want the warning with its numbers", row.Dispositions)
	}
}

func strategyBodyCarriesDetail(raw any, detail string) bool {
	list, _ := raw.([]any)
	for _, entry := range list {
		disposition, _ := entry.(map[string]any)
		if disposition["reason"] == "NO_DATA_TRIGGER_BEYOND_HORIZON" && disposition["detail"] == detail {
			return true
		}
	}
	return false
}

// A withheld strategy that also carries a note on its configuration reads its
// diagnosis from what withheld it: the note is listed, after the withheld
// item, and never stands in for it. The catalog may list the note first; the
// row's verdict, reason and words are the withheld item's all the same.
func TestAWithheldStrategysDiagnosisReadsTheWithheldItemBeforeAnyNote(t *testing.T) {
	facts := diagnosisFacts()
	facts["4111"] = StrategyLookupFacts{Available: true, Found: true, Publication: StrategyPublication{SnapshotRevision: "s1", Epoch: 7},
		Dispositions: []StrategyDisposition{
			{Scope: "PLAN", Disposition: "CONFIG_NOTED", Reason: "NO_DATA_TRIGGER_BEYOND_HORIZON",
				FieldPath: "items[0].no_data_config.continuous", Detail: "continuous=11 period=60 earliest_alert_after=600 tracking_horizon=600 horizon_source=PLATFORM"},
			{Scope: "PLAN", Disposition: "UNSUPPORTED_PHASE2_CAPABILITY", Reason: "ALGORITHM_NOT_MIGRATED", FieldPath: "items[0].algorithms[0]"}}}
	rig := newDiagnosisRig(t, facts, nil)
	rig.universe = []string{"4111"}
	page := rig.page(t, "", 0)
	if len(page.Strategies) != 1 {
		t.Fatalf("diagnosis rows = %+v, want the one strategy", page.Strategies)
	}
	row := page.Strategies[0]
	if row.Verdict != StateNotDetecting || row.Reason != "ALGORITHM_NOT_MIGRATED" {
		t.Fatalf("diagnosis = %s/%s, want NOT_DETECTING for the algorithm that withheld it", row.Verdict, row.Reason)
	}
	if len(row.Dispositions) != 2 || row.Dispositions[0].Reason != "ALGORITHM_NOT_MIGRATED" ||
		row.Dispositions[1].Reason != "NO_DATA_TRIGGER_BEYOND_HORIZON" {
		t.Fatalf("dispositions = %+v, want the withheld item first and the note after it", row.Dispositions)
	}
}
