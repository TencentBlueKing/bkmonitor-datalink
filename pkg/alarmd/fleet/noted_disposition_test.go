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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// A noted item runs, so it is served and not withheld: CONFIG_NOTED is in
// isWithheld's known list beside ACCEPTED, and a word the list does not
// know is withheld, which is the side a new refusal should land on. The
// words are written out here rather than read from the constants, so a
// rename on one side only fails this test instead of moving both halves.
func TestANotedDispositionIsServedNotWithheld(t *testing.T) {
	for disposition, withheld := range map[string]bool{
		"ACCEPTED":         false,
		"CONFIG_NOTED":     false,
		"CONFIG_REJECTED":  true,
		"STALE_CONFIG":     true,
		"A_WORD_NOT_KNOWN": true,
	} {
		if got := isWithheld(disposition); got != withheld {
			t.Errorf("isWithheld(%q) = %v, want %v", disposition, got, withheld)
		}
	}
	// The control plane gives the noted record the word the fleet knows.
	if got := string(controlplane.DispositionConfigNoted); got != "CONFIG_NOTED" {
		t.Fatalf("the control plane writes %q for a noted item, the fleet knows CONFIG_NOTED", got)
	}

	// And on the counts a reader sees: a strategy with a note is among the
	// accepted, not among the withheld, on the source line and its standing.
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	source := NewSourceFacts(at, map[string]int{"ACCEPTED": 2, "CONFIG_NOTED": 1},
		[]WithheldObject{{StrategyID: "11", Scope: "PLAN", Disposition: "CONFIG_NOTED", Reason: "NO_DATA_TRIGGER_BEYOND_HORIZON"}})
	if source.Listed != 2 || source.Accepted != 2 {
		t.Fatalf("listed=%d accepted=%d, want both strategies listed and accepted, the noted one included", source.Listed, source.Accepted)
	}
	standing := sourceStandingOf(source, 2)
	if standing.Listed-standing.Accepted != 0 || standing.Noted != 1 {
		t.Fatalf("standing = %+v, want none withheld and one noted", standing)
	}
	for _, report := range ReportChecks(nil, nil, &View{Source: source, SourceReplica: "pod-a"}, at) {
		if report.Code != CheckConfigNoted && report.Strategies > 0 {
			t.Errorf("the noted strategy is counted under %s", report.Code)
		}
	}
}
