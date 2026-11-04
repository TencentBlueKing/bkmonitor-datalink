// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"regexp"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sourceliveness"
)

// The verdict counter's cells exist from start: every source kind, alive
// once, unknown by each of its five reasons and unsupported by each of its
// own - the reasons the probe decides by, named in the help. A verdict adds
// to its cell; one outside the sets adds nothing.
func TestTheSourceLivenessCounterHasEveryCellAndNamesEveryReason(t *testing.T) {
	const checks = "alarmd_source_liveness_checks_total"
	r := NewRecorder(BuildInfo{})
	cells := map[[3]string]float64{}
	for _, m := range gatherFamily(t, r, checks) {
		var key [3]string
		for _, label := range m.Label {
			switch label.GetName() {
			case "source_kind":
				key[0] = label.GetValue()
			case "result":
				key[1] = label.GetValue()
			case "reason":
				key[2] = label.GetValue()
			}
		}
		cells[key] = m.GetCounter().GetValue()
	}
	unknown := []string{"status", "partial", "timeout", "error", "empty_without_status"}
	unsupported := []string{"promql", "bkdata", "apm", "multi_table", "data_label", "event_sparse"}
	for _, kind := range []string{"log", "event", "time_series"} {
		if _, ok := cells[[3]string{kind, "alive", ""}]; !ok {
			t.Errorf("no %s alive cell", kind)
		}
		for _, reason := range unknown {
			if _, ok := cells[[3]string{kind, "unknown", reason}]; !ok {
				t.Errorf("no %s unknown %s cell", kind, reason)
			}
		}
		for _, reason := range unsupported {
			if _, ok := cells[[3]string{kind, "unsupported", reason}]; !ok {
				t.Errorf("no %s unsupported %s cell", kind, reason)
			}
		}
	}
	if want := 3 * (1 + len(unknown) + len(unsupported)); len(cells) != want {
		t.Fatalf("%d cells, want %d: %v", len(cells), want, cells)
	}
	help := familyHelp(t, r, checks)
	for _, reason := range append(append([]string{}, unknown...), unsupported...) {
		if !regexp.MustCompile(`(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(reason) + `([^A-Za-z0-9_]|$)`).MatchString(help) {
			t.Errorf("the help does not name %q", reason)
		}
	}

	r.SourceLivenessChecked(sourceliveness.KindLog, sourceliveness.Verdict{Result: sourceliveness.ResultUnknown, Reason: sourceliveness.ReasonStatus})
	r.SourceLivenessChecked(sourceliveness.KindLog, sourceliveness.Verdict{Result: sourceliveness.ResultUnknown, Reason: "pending"})
	r.SourceLivenessChecked("mixed", sourceliveness.Verdict{Result: sourceliveness.ResultAlive})
	r.SourceLivenessProbed(sourceliveness.KindTimeSeries, 30*time.Millisecond)
	moved := 0.0
	for _, m := range gatherFamily(t, r, checks) {
		moved += m.GetCounter().GetValue()
	}
	if moved != 1 {
		t.Fatalf("counted %v, want the one verdict inside the sets", moved)
	}
	if deferred := gatherFamily(t, NewRecorder(BuildInfo{}), "alarmd_source_liveness_probe_deferred_total"); len(deferred) != 1 ||
		deferred[0].GetCounter().GetValue() != 0 {
		t.Fatalf("deferred before any probe = %v, want its one cell at 0", deferred)
	}
	if evicted := gatherFamily(t, NewRecorder(BuildInfo{}), "alarmd_source_liveness_answers_evicted_total"); len(evicted) != 1 ||
		evicted[0].GetCounter().GetValue() != 0 {
		t.Fatalf("evicted before any answer = %v, want its one cell at 0", evicted)
	}
	r.SourceLivenessEvicted()
	if held := gatherFamily(t, NewRecorder(BuildInfo{}), "alarmd_source_liveness_held_evicted_total"); len(held) != 1 ||
		held[0].GetCounter().GetValue() != 0 {
		t.Fatalf("held evicted before any = %v, want its one cell at 0", held)
	}
	r.SourceLivenessHeldEvicted()
	if held := gatherFamily(t, r, "alarmd_source_liveness_held_evicted_total"); len(held) != 1 || held[0].GetCounter().GetValue() != 1 {
		t.Fatalf("held evicted = %v, want 1", held)
	}
	if evicted := gatherFamily(t, r, "alarmd_source_liveness_answers_evicted_total"); len(evicted) != 1 || evicted[0].GetCounter().GetValue() != 1 {
		t.Fatalf("evicted = %v, want 1", evicted)
	}
	r.SourceLivenessDeferred(sourceliveness.KindLog)
	if deferred := gatherFamily(t, r, "alarmd_source_liveness_probe_deferred_total"); len(deferred) != 1 || deferred[0].GetCounter().GetValue() != 1 {
		t.Fatalf("deferred = %v, want 1", deferred)
	}
	for _, m := range gatherFamily(t, r, "alarmd_source_liveness_check_seconds") {
		if m.Label[0].GetValue() == "time_series" && m.GetHistogram().GetSampleCount() != 1 {
			t.Fatalf("time_series probes %d, want 1", m.GetHistogram().GetSampleCount())
		}
	}
}

func familyHelp(t *testing.T, r *Recorder, name string) string {
	t.Helper()
	families, err := r.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == name {
			return family.GetHelp()
		}
	}
	t.Fatalf("no family %s", name)
	return ""
}
