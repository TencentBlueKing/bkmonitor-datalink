// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// Sets that carry none of this process's alerts are one scrape away: the
// two split counts side by side and the state as its own gauge, both
// present at zero before anything is sent, so "none found" cannot be read
// off a missing series.
func TestOurAlertsAgainstTheSetsAreScrapedAsTwoCountsAndAState(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	stats := openalerts.Stats{Mode: openalerts.ModeSelfMaintained}
	r.SetOpenAlertSetSource(func() openalerts.Stats { return stats })
	read := func() (map[string]float64, float64) {
		sent := map[string]float64{}
		for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_open_alert_set_sent_alerts") {
			sent[m.Label[0].GetValue()] = m.GetGauge().GetValue()
		}
		state := gatherFamily(t, r, "bkmonitor_alarmd_open_alert_set_disjoint")
		if len(state) != 1 {
			t.Fatalf("disjoint series = %d, want exactly one", len(state))
		}
		return sent, state[0].GetGauge().GetValue()
	}
	sent, disjoint := read()
	if len(sent) != 2 || sent["yes"] != 0 || sent["no"] != 0 || disjoint != 0 {
		t.Fatalf("before any send: sent %v disjoint %v, want yes and no at zero and 0", sent, disjoint)
	}
	stats.SentInSet, stats.SentNotInSet, stats.Disjoint = 0, 103, true
	stats.Unavailable = map[openalerts.UnavailableReason]uint64{openalerts.UnavailableMembersDisjoint: 1}
	sent, disjoint = read()
	if sent["yes"] != 0 || sent["no"] != 103 || disjoint != 1 {
		t.Fatalf("sets carrying none of ours: sent %v disjoint %v", sent, disjoint)
	}
	reasons := map[string]float64{}
	for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_open_alert_set_unavailable_total") {
		reasons[m.Label[0].GetValue()] = m.GetCounter().GetValue()
	}
	if reasons["members_disjoint"] != 1 {
		t.Fatalf("unavailable reasons = %v, want members_disjoint at 1", reasons)
	}
}
