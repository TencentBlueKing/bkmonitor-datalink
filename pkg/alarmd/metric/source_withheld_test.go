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

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// sourceWithheldSeries reads the family back out of a registry, by label.
func sourceWithheldSeries(t *testing.T, metrics phaseTwoMetrics) map[string]float64 {
	t.Helper()
	registry := prometheus.NewRegistry()
	registry.MustRegister(metrics.sourceWithheldLines)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	read := map[string]float64{}
	for _, family := range families {
		for _, series := range family.Metric {
			for _, pair := range series.Label {
				if pair.GetName() == "result" {
					read[pair.GetValue()] = series.GetCounter().GetValue()
				}
			}
		}
	}
	return read
}

// withheldLine is the observation the leader's refresh writes per named object.
func withheldLine(dropped int) observability.Observation {
	return observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageSourceWithheld,
		Result: observability.ResultSuccess,
		SourceWithheld: &observability.SourceWithheldFacts{
			Disposition: "CONFIG_REJECTED", Reason: "PLAN_INVALID", Dropped: dropped,
		},
	}
}

// Both results exist from the moment the process starts.
//
// dropped is expected to stay at zero for the life of most deployments, and a
// zero nobody can tell from a label nothing ever wrote says nothing at all -
// which is the whole reading this family is for.
func TestBothWithheldLineResultsHaveASeriesBeforeAnythingHappens(t *testing.T) {
	read := sourceWithheldSeries(t, newPhaseTwoMetrics())
	if len(read) != len(sourceWithheldLineResults) {
		t.Fatalf("series = %+v, want one per result (%d)", read, len(sourceWithheldLineResults))
	}
	for _, result := range sourceWithheldLineResults {
		value, present := read[result]
		if !present {
			t.Fatalf("result %q has no series before anything happened", result)
		}
		if value != 0 {
			t.Fatalf("result %q starts at %v, want 0", result, value)
		}
	}
}

// Each named line is one, and the cut is added as the objects it stands for.
//
// Counting the cut as one would say "this round dropped something" and lose
// how much, which is the difference between a deployment with eleven problems
// and one with eleven thousand.
func TestWithheldLinesCountNamedObjectsAndTheObjectsTheBudgetCut(t *testing.T) {
	metrics := newPhaseTwoMetrics()
	metrics.observe(withheldLine(0))
	metrics.observe(withheldLine(0))
	metrics.observe(withheldLine(40))

	read := sourceWithheldSeries(t, metrics)
	if read[sourceWithheldLineNamed] != 3 {
		t.Fatalf("named = %v, want the 3 lines that were written", read[sourceWithheldLineNamed])
	}
	if read[sourceWithheldLineDropped] != 40 {
		t.Fatalf("dropped = %v, want the 40 objects the budget cut, not the one line that reported them",
			read[sourceWithheldLineDropped])
	}
}

// A round that named everything reports a computed zero, not an absent one.
func TestAWithheldRoundThatFittedReportsZeroDropped(t *testing.T) {
	metrics := newPhaseTwoMetrics()
	metrics.observe(withheldLine(0))

	read := sourceWithheldSeries(t, metrics)
	if read[sourceWithheldLineNamed] != 1 {
		t.Fatalf("named = %v, want 1", read[sourceWithheldLineNamed])
	}
	if read[sourceWithheldLineDropped] != 0 {
		t.Fatalf("dropped = %v, want a reported zero", read[sourceWithheldLineDropped])
	}
}

// Only the control plane's withheld stage moves this family.
//
// Without the stage in the guard, every control-plane observation carrying
// these facts would count, and the family would stop being a count of lines.
func TestOnlyTheWithheldStageMovesTheLineCount(t *testing.T) {
	metrics := newPhaseTwoMetrics()
	elsewhere := withheldLine(7)
	elsewhere.Stage = observability.StageSnapshotRefreshed
	metrics.observe(elsewhere)
	other := withheldLine(7)
	other.Component = observability.ComponentEvaluation
	metrics.observe(other)
	// And a withheld observation carrying no facts at all.
	metrics.observe(observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageSourceWithheld,
		Result: observability.ResultSuccess,
	})

	for result, value := range sourceWithheldSeries(t, metrics) {
		if value != 0 {
			t.Fatalf("result %q moved to %v on an observation that is not a withheld line", result, value)
		}
	}
}
