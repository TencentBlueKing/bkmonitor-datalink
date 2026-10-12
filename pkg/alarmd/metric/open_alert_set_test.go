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
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// Every outcome of the second gate is created at zero, because the one that
// matters most at zero is not_configured: on a production worker it is the
// wiring having come apart, and an absent series would hide exactly that.
func TestOpenAlertGateCounterStartsAtZeroForEveryOutcomeAndAddsRecords(t *testing.T) {
	const name = "bkmonitor_alarmd_trigger_open_alert_gate_total"
	r := NewRecorder(BuildInfo{})
	initial := gatherFamily(t, r, name)
	if len(initial) != len(observability.OpenAlertGateOutcomes) {
		t.Fatalf("outcomes before any observation = %d series, want all %d created at zero", len(initial), len(observability.OpenAlertGateOutcomes))
	}
	for _, m := range initial {
		if m.GetCounter().GetValue() != 0 {
			t.Fatalf("outcome before any observation = %v, want 0", m)
		}
	}
	r.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageEvaluationCompleted,
		OpenAlertGates: []observability.OpenAlertGateFact{
			{Outcome: observability.OpenAlertGateHeldNoOpenAlert, Records: 3},
			{Outcome: observability.OpenAlertGatePassed, Records: 1},
			{Outcome: observability.OpenAlertGatePassedOneTimeClose, Records: 2},
		},
	})
	got := map[string]float64{}
	for _, m := range gatherFamily(t, r, name) {
		got[m.Label[0].GetValue()] = m.GetCounter().GetValue()
	}
	if got["held_no_open_alert"] != 3 || got["passed"] != 1 || got["passed_one_time_close"] != 2 || got["not_configured"] != 0 {
		t.Fatalf("outcomes after observation = %v", got)
	}
}

// The copy's state is scraped, not pushed: every answer and reason is a
// series from the start, and the calibration age does not exist as a series
// until a calibration has completed.
func TestOpenAlertSetCollectorEmitsEveryWordAndTheAgeOnlyOnceCalibrated(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	stats := openalerts.Stats{Lookups: map[openalerts.Answer]uint64{openalerts.AnswerSelfMaintained: 4},
		NoticesRefused: map[openalerts.NoticeRefusal]uint64{openalerts.NoticeUndecodable: 5},
		Unavailable:    map[openalerts.UnavailableReason]uint64{openalerts.UnavailableReadError: 2}, Refreshes: map[string]uint64{"unavailable": 2}}
	r.SetOpenAlertSetSource(func() openalerts.Stats { return stats })
	now := time.Unix(1_700_000_600, 0)
	r.phaseTwo.openAlertSet.now = func() time.Time { return now }

	if age := gatherFamily(t, r, "bkmonitor_alarmd_open_alert_set_authoritative_age_seconds"); len(age) != 0 {
		t.Fatalf("age series before any calibration = %v, want none", age)
	}
	lookups := map[string]float64{}
	for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_open_alert_set_lookup_total") {
		lookups[m.Label[0].GetValue()] = m.GetCounter().GetValue()
	}
	if len(lookups) != len(openalerts.Answers) || lookups["self_maintained"] != 4 || lookups["passed_through"] != 0 {
		t.Fatalf("lookups = %v, want every answer present and self_maintained at 4", lookups)
	}
	unavailable := map[string]float64{}
	for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_open_alert_set_unavailable_total") {
		unavailable[m.Label[0].GetValue()] = m.GetCounter().GetValue()
	}
	if len(unavailable) != len(openalerts.UnavailableReasons) || unavailable["read_error"] != 2 || unavailable["location_unconfirmed"] != 0 {
		t.Fatalf("unavailable = %v", unavailable)
	}

	refused := map[string]float64{}
	for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_open_alert_set_notices_refused_total") {
		refused[m.Label[0].GetValue()] = m.GetCounter().GetValue()
	}
	if want := map[string]float64{"oversized": 0, "undecodable": 5, "invalid_key": 0}; !reflect.DeepEqual(refused, want) {
		t.Fatalf("refused notices = %v, want %v", refused, want)
	}

	stats.LoadedAt = now.Add(-90 * time.Second)
	age := gatherFamily(t, r, "bkmonitor_alarmd_open_alert_set_authoritative_age_seconds")
	if len(age) != 1 || age[0].GetGauge().GetValue() != 90 {
		t.Fatalf("age after a calibration = %v, want one series at 90", age)
	}
}
