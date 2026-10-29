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
	"testing"
	"time"
)

// A raised no-data group bound is a cost its owner chose, not a part of the
// strategy that runs differently: the strategy reads supported on its row and
// in every area of the capability count, while a reading note beside it still
// reads partial.
func TestANoDataGroupBoundNoteLeavesTheStrategySupported(t *testing.T) {
	note := StrategyDisposition{Scope: "PLAN", Disposition: "CONFIG_NOTED", Reason: "NO_DATA_GROUP_BOUND_ABOVE_DEFAULT"}
	row := DiagnosisRow{Dispositions: []DiagnosisDisposition{{StrategyDisposition: note}}}
	if got := capabilityOf(row); got != CapabilitySupported {
		t.Fatalf("a strategy noted for its group bound reads %s, want supported", got)
	}
	count := CountCapability([]CapabilityRecord{
		{StrategyID: "11", Scope: "PLAN", Disposition: "CONFIG_NOTED", Reason: "NO_DATA_GROUP_BOUND_ABOVE_DEFAULT"},
		{StrategyID: "12", Scope: "PLAN", Disposition: "CONFIG_NOTED", Reason: "AGG_INTERVAL_DEFAULTED"},
	}, 2)
	for _, cell := range count.Cells {
		want := 0
		switch {
		case cell.Outcome == CapabilitySupported && cell.Area == CapabilityConfig:
			want = 1
		case cell.Outcome == CapabilitySupported:
			want = 2
		case cell.Outcome == CapabilityPartial && cell.Area == CapabilityConfig:
			want = 1
		}
		if cell.Strategies != want {
			t.Errorf("cell %s/%s counts %d, want %d", cell.Area, cell.Outcome, cell.Strategies, want)
		}
	}
	for _, reason := range count.Reasons {
		if reason.Code == "NO_DATA_GROUP_BOUND_ABOVE_DEFAULT" {
			t.Fatalf("the cost note is listed as a capability reason: %+v", reason)
		}
	}
}

// The note asks nobody to act: a CONFIG_NOTED line holding only it is
// nobody's, so an owner's deliberate cost does not read as an edit to make.
func TestANoDataGroupBoundNoteAsksNobodyToAct(t *testing.T) {
	if words := WithheldWordsOf("NO_DATA_GROUP_BOUND_ABOVE_DEFAULT"); words.Action != ActionNone {
		t.Fatalf("the note's action is %q, want none", words.Action)
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	withheld := []WithheldObject{{StrategyID: "11", Scope: "PLAN", Disposition: "CONFIG_NOTED", Reason: "NO_DATA_GROUP_BOUND_ABOVE_DEFAULT"}}
	view := View{Source: NewSourceFacts(at, map[string]int{"ACCEPTED": 1, "CONFIG_NOTED": 1}, withheld), SourceReplica: "pod-a"}
	for _, report := range ReportChecks(nil, nil, &view, at) {
		if report.Code == CheckConfigNoted {
			if report.Owner != OwnerNobody {
				t.Fatalf("a line holding only the note is %s's, want nobody's", report.Owner)
			}
			return
		}
	}
	t.Fatal("no CONFIG_NOTED line")
}

// On the board the note decides nothing: a row's action comes from its
// checks, and the note raises none, so a detecting strategy whose only extra
// is the note has urgency none and no reason sentence. The note's own words
// asking nobody to act is held by the case above.
func TestANoDataGroupBoundNoteLeavesTheBoardRowWithNothingToDo(t *testing.T) {
	facts := diagnosisFacts()
	facts["4120"] = StrategyLookupFacts{Available: true, Found: true, Publication: StrategyPublication{SnapshotRevision: "s1", Epoch: 7},
		Plans: plans(planA),
		Dispositions: []StrategyDisposition{
			{Scope: "PLAN", Disposition: "ACCEPTED"},
			{Scope: "PLAN", Disposition: "CONFIG_NOTED", Reason: "NO_DATA_GROUP_BOUND_ABOVE_DEFAULT",
				FieldPath: "items[0].no_data_config.max_groups", Detail: "max_groups=200000 default=100000"}}}
	rig := newDiagnosisRig(t, facts, nil)
	rig.universe = []string{"4120"}
	page := rig.page(t, "", 0)
	if len(page.Strategies) != 1 || page.Strategies[0].Verdict != StateDetecting {
		t.Fatalf("diagnosis rows = %+v, want the one strategy detecting", page.Strategies)
	}
	row := page.Strategies[0]
	if urgency, reason := urgencyOf(row), strategyReasonOf(row, time.Now()); urgency != UrgencyNone || reason != nil {
		t.Fatalf("urgency %s reason %+v, want none and no reason: the owner chose this cost", urgency, reason)
	}
}
