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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func gapGuardScopeRound(reason, progress string) observability.Observation {
	return observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageGapGuardProgress,
		Result: observability.ResultSuccess, Direction: observability.DirectionInternal,
		GapProgress: &observability.GapProgressFacts{
			Scope: "plan", Status: "GAPPED", Reason: reason, Required: 5, Progress: progress,
		},
	}
}

func gapGuardSeries(t *testing.T, recorder *Recorder) map[[3]string]float64 {
	t.Helper()
	families, err := recorder.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	series := map[[3]string]float64{}
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_worker_gap_guard_scope_rounds_total" {
			continue
		}
		for _, sample := range family.Metric {
			key := [3]string{}
			for _, pair := range sample.Label {
				switch pair.GetName() {
				case "status":
					key[0] = pair.GetValue()
				case "reason":
					key[1] = pair.GetValue()
				case "progress":
					key[2] = pair.GetValue()
				}
			}
			series[key] = sample.GetCounter().GetValue()
		}
	}
	return series
}

// A reason this build does not name lands on the catch-all rather than making
// a series of its own.
//
// The reason comes off a persisted marker, which a build that is not this one
// may have written, so the label's input is open however closed this build's
// vocabulary is. Without the bound, a rolling upgrade that introduces a reason
// -- or a corrupted marker -- adds series to a family nobody is watching the
// cardinality of, and the only sign is the scrape getting slower.
func TestAnUnnamedGapReasonIsCountedUnderTheCatchAll(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	before := len(gapGuardSeries(t, recorder))

	recorder.Observe(context.Background(),
		gapGuardScopeRound("SOMETHING_A_LATER_BUILD_ADDED", contract.GapScopeProgressNone))

	after := gapGuardSeries(t, recorder)
	if len(after) != before {
		t.Fatalf("an unnamed reason added %d series to a family bounded at %d: the label is not bounded, "+
			"and the marker it came off may have been written by another build",
			len(after)-before, before)
	}
	key := [3]string{"GAPPED", contract.GapScopeReasonOther, contract.GapScopeProgressNone}
	if after[key] != 1 {
		t.Fatalf("the catch-all series holds %v, want the round counted under it; series = %v",
			after[key], after)
	}
}

// A reason this build does name keeps its own series, so the catch-all means
// what it says.
func TestANamedGapReasonKeepsItsOwnSeries(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(),
		gapGuardScopeRound(contract.ReasonConfigDrift, contract.GapScopeProgressNone))

	series := gapGuardSeries(t, recorder)
	named := [3]string{"GAPPED", contract.ReasonConfigDrift, contract.GapScopeProgressNone}
	catchAll := [3]string{"GAPPED", contract.GapScopeReasonOther, contract.GapScopeProgressNone}
	if series[named] != 1 {
		t.Fatalf("the named series holds %v, want the round; series = %v", series[named], series)
	}
	if series[catchAll] != 0 {
		t.Fatalf("a named reason was counted as other: %v", series[catchAll])
	}
}

// Every combination has a series before anything is observed, so a zero is a
// zero rather than a label nothing ever wrote.
//
// It matters most for the reading this family exists for: no scope held at all
// is the healthy state, and a reader who cannot tell it from "nobody is
// reporting" learns nothing from an empty chart.
func TestEveryGapGuardCombinationIsPreCreated(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	series := gapGuardSeries(t, recorder)
	want := 2 * (len(contract.GapScopeReasons()) + 1) * len(contract.GapScopeProgressValues)
	if len(series) != want {
		t.Fatalf("pre-created %d series, want %d: status x (reason + other) x progress", len(series), want)
	}
	for _, value := range series {
		if value != 0 {
			t.Fatalf("a pre-created series started at %v, want zero", value)
		}
	}
}
