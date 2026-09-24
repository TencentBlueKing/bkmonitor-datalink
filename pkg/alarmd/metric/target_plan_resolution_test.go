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
	"sort"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The selector cells an operator acts on exist at zero before any Plan has
// resolved, and they are asserted as one set: the object page separates
// "no reference has ever crossed a business" from "the mechanism is not
// wired" by reading a zero, and with the series absent those two states
// look the same. One assertion over the whole set, rather than one per cell,
// so that an eighth cell added later without a test still fails here.
func TestTheSelectorCellsAnOperatorActsOnArePublishedAtZeroAsOneSet(t *testing.T) {
	r := NewRecorder(BuildInfo{})

	// Spelled out rather than built from targetplan's constants: this is the
	// set an operator queries by these words, so a constant renamed must not
	// move it and a constant's value changed must fail it. The resolver's
	// own tests go through the constants and are blind to the value.
	want := []string{
		"dynamic_group|Unavailable|key_missing",
		"dynamic_group|Unavailable|read_failed",
		"dynamic_group|Unavailable|stale",
		"dynamic_group|Incomplete|members_dropped",
		"dynamic_topology|Unavailable|index_unavailable",
		"dynamic_topology|OKEmpty|node_missing",
		"dynamic_topology|OKEmpty|node_in_other_business",
		"static|Unavailable|model_representation_unresolved",
	}
	sort.Strings(want)
	got := selectorCells(t, r)
	cells := make([]string, 0, len(got))
	for cell, value := range got {
		cells = append(cells, cell)
		if value != 0 {
			t.Fatalf("%s before any resolution = %v, want 0", cell, value)
		}
	}
	sort.Strings(cells)
	if !reflect.DeepEqual(cells, want) {
		t.Fatalf("selector cells before any resolution:\n got %v\nwant %v", cells, want)
	}

	states := map[string]float64{}
	for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_target_plan_resolution_total") {
		states[m.Label[0].GetValue()] = m.GetCounter().GetValue()
	}
	if !reflect.DeepEqual(states, map[string]float64{"Complete": 0, "Incomplete": 0, "Unavailable": 0}) {
		t.Fatalf("resolution states before any resolution = %v, want the three at zero", states)
	}
}

// A resolution counts once for the Plan and once per selector; a cell that
// was not created at zero appears on its first observation, and a word off
// the closed lists lands on other rather than opening a series.
func TestAResolutionCountsThePlanOnceAndEachSelectorOnce(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	r.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentAccess, Stage: observability.StageTargetResolved,
		TargetResolution: &observability.TargetResolutionFacts{
			StrategyID: "7", State: "Unavailable",
			Selectors: []observability.TargetSelectorFacts{
				{Kind: "static", State: "OK", Reason: "none", Kept: 2},
				{Kind: "dynamic_topology", State: "OKEmpty", Reason: "node_in_other_business", NodeForeign: true},
				{Kind: "dynamic_group", State: "Unavailable", Reason: "key_missing"},
				{Kind: "dynamic_group", State: "Unavailable", Reason: "key_missing"},
				{Kind: "dynamic_group", State: "Unavailable", Reason: "not_a_reason"},
			},
		},
	})

	got := selectorCells(t, r)
	for cell, want := range map[string]float64{
		"static|OK|none": 1,
		"dynamic_topology|OKEmpty|node_in_other_business": 1,
		"dynamic_group|Unavailable|key_missing":           2,
		"dynamic_group|Unavailable|other":                 1,
		"dynamic_topology|OKEmpty|node_missing":           0,
	} {
		if got[cell] != want {
			t.Fatalf("%s after one resolution = %v, want %v (all: %v)", cell, got[cell], want, got)
		}
	}
	states := map[string]float64{}
	for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_target_plan_resolution_total") {
		states[m.Label[0].GetValue()] = m.GetCounter().GetValue()
	}
	if states["Unavailable"] != 1 || states["Complete"] != 0 || states["Incomplete"] != 0 {
		t.Fatalf("resolution states after one Unavailable resolution = %v", states)
	}
}

// selectorCells reads target_selector_resolutions_total as kind|state|reason
// to value, in the label order the metric declares.
func selectorCells(t *testing.T, r *Recorder) map[string]float64 {
	t.Helper()
	cells := map[string]float64{}
	for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_target_selector_resolutions_total") {
		labels := map[string]string{}
		for _, pair := range m.Label {
			labels[pair.GetName()] = pair.GetValue()
		}
		cells[labels["kind"]+"|"+labels["state"]+"|"+labels["reason"]] = m.GetCounter().GetValue()
	}
	return cells
}
