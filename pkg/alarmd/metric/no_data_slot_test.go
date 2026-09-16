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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// noDataSlotSeries reads the outcome family back out of a registry, by label.
func noDataSlotSeries(t *testing.T, metrics phaseTwoMetrics) map[string]float64 {
	t.Helper()
	registry := prometheus.NewRegistry()
	registry.MustRegister(metrics.noDataSlotPlans)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	read := map[string]float64{}
	for _, family := range families {
		for _, series := range family.Metric {
			for _, pair := range series.Label {
				if pair.GetName() == "outcome" {
					read[pair.GetValue()] = series.GetCounter().GetValue()
				}
			}
		}
	}
	return read
}

// All four outcomes exist from the moment the process starts.
//
// This is the difference between "no Plan hit that outcome" and "nothing ever
// wrote that label", and for one of them it is the whole reading: a steady zero
// on SKIPPED_SLOT_BUDGET is what says the Slot is carrying its no-data work,
// and a label that never appears says the same thing to anyone scraping while
// saying nothing at all.
func TestEveryNoDataOutcomeHasASeriesBeforeAnythingHappens(t *testing.T) {
	read := noDataSlotSeries(t, newPhaseTwoMetrics())
	if len(read) != len(nodata.SlotOutcomes) {
		t.Fatalf("series = %+v, want one per outcome (%d)", read, len(nodata.SlotOutcomes))
	}
	for _, outcome := range nodata.SlotOutcomes {
		value, present := read[string(outcome)]
		if !present {
			t.Fatalf("outcome %q has no series before anything happened; a zero and a missing label "+
				"read the same to whoever is scraping", outcome)
		}
		if value != 0 {
			t.Fatalf("outcome %q starts at %v, want 0", outcome, value)
		}
	}
}

// The count is the Plans that landed on that outcome, not the observations.
//
// One observation carries a whole Slot's worth of one outcome, so reading it as
// one would under-report by however many Plans shared it - and under-report is
// the direction nobody checks, because the number still moves.
func TestTheNoDataOutcomeCountIsPlansNotObservations(t *testing.T) {
	metrics := newPhaseTwoMetrics()
	metrics.observeNoDataSlot(observability.Observation{
		NoDataSlot: &observability.NoDataSlotFacts{Outcome: string(nodata.OutcomeEvaluated), Plans: 7},
	})
	metrics.observeNoDataSlot(observability.Observation{
		NoDataSlot: &observability.NoDataSlotFacts{Outcome: string(nodata.OutcomeSkippedQueryNotFull), Plans: 2},
	})

	read := noDataSlotSeries(t, metrics)
	if read[string(nodata.OutcomeEvaluated)] != 7 {
		t.Fatalf("EVALUATED = %v, want the 7 Plans the observation carried", read[string(nodata.OutcomeEvaluated)])
	}
	if read[string(nodata.OutcomeSkippedQueryNotFull)] != 2 {
		t.Fatalf("SKIPPED_QUERY_NOT_FULL = %v, want 2", read[string(nodata.OutcomeSkippedQueryNotFull)])
	}
	// And the outcomes nothing reported are still there, still zero.
	if read[string(nodata.OutcomeSkippedSlotBudget)] != 0 {
		t.Fatalf("SKIPPED_SLOT_BUDGET = %v, want a reported zero", read[string(nodata.OutcomeSkippedSlotBudget)])
	}
}

// An observation with nothing in it moves nothing, so a Slot with no no-data
// Plan does not make the family look like it reported.
func TestANoDataObservationWithoutFactsMovesNothing(t *testing.T) {
	metrics := newPhaseTwoMetrics()
	metrics.observeNoDataSlot(observability.Observation{})
	metrics.observeNoDataSlot(observability.Observation{
		NoDataSlot: &observability.NoDataSlotFacts{Outcome: string(nodata.OutcomeEvaluated), Plans: 0},
	})
	for outcome, value := range noDataSlotSeries(t, metrics) {
		if value != 0 {
			t.Fatalf("outcome %q moved to %v on an observation carrying no Plans", outcome, value)
		}
	}
}
