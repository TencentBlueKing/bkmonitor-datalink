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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The no-data observation reaches the counters through the recorder the process
// actually installs, not by being handed to the counter.
//
// Everything between the worker and the series is a place the observation can
// be dropped without anything failing. NormalizeComponentStage folds a pair it
// does not know to (_other, _other), which is a whitelist over an open input,
// and a counter fed from observations then sits at a computed zero forever --
// the same reading as a worker with nothing to report. The unit tests below
// hand the observation straight to the metric struct and cannot see any of it.
//
// So this goes in the front door: Recorder.Observe, the same call the runtime
// makes, and reads the series out of the registry the scrape reads.
func TestNoDataObservationsReachTheCountersThroughTheRecorder(t *testing.T) {
	recorder := NewRecorder(BuildInfo{Version: "0.2.9999", Commit: "0123456789abcdef", SchemaVersion: "v3"})
	ctx := context.Background()

	recorder.Observe(ctx, observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageNoDataDecided,
		Direction: observability.DirectionInternal, Result: observability.ResultSuccess,
		NoDataCensus: &observability.NoDataCensusFacts{Hop: observability.NoDataHopDue, Plans: 9},
	})
	recorder.Observe(ctx, observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageNoDataDecided,
		Direction: observability.DirectionInternal, Result: observability.ResultSuccess,
		NoDataSlot: &observability.NoDataSlotFacts{Outcome: string(nodata.OutcomeEvaluated), Plans: 9},
	})

	if got := recorderCounterValue(t, recorder, "bkmonitor_alarmd_worker_no_data_plans_seen_total"); got != 9 {
		t.Fatalf("census = %v through the recorder, want 9. Between the worker and the series the "+
			"observation passes a component/stage whitelist that folds what it does not know to "+
			"(_other, _other); a counter fed from observations then reads as a computed zero, which is "+
			"exactly what a worker with nothing to report reads as", got)
	}
	series := noDataSlotSeriesFrom(t, recorder)
	if series[string(nodata.OutcomeEvaluated)] != 9 {
		t.Fatalf("EVALUATED = %v through the recorder, want 9", series[string(nodata.OutcomeEvaluated)])
	}
}

// The pair the worker emits is one the observation catalog names.
//
// Stated against the normalizer itself, so the failure says which of the two
// halves is unregistered rather than only that a counter did not move.
func TestTheNoDataComponentStageSurvivesNormalisation(t *testing.T) {
	component, stage := observability.NormalizeComponentStage(
		observability.ComponentEvaluation, observability.StageNoDataDecided,
	)
	if component != observability.ComponentEvaluation || stage != observability.StageNoDataDecided {
		t.Fatalf("(%q, %q) normalised to (%q, %q): the pair the worker emits is not in the observation "+
			"catalog, so every no-data observation is folded away before it reaches a counter or a log",
			observability.ComponentEvaluation, observability.StageNoDataDecided, component, stage)
	}
}

// A Slot with no such Plan reports its zero.
//
// Skipping the census on an empty Slot would put the number back where it
// started: absent and zero would mean the same thing, and the question this
// answers is exactly which of the two a worker is in.
func TestASlotWithNoNoDataPlansStillReportsItsCensus(t *testing.T) {
	recorder := NewRecorder(BuildInfo{Version: "0.2.9999", Commit: "0123456789abcdef", SchemaVersion: "v3"})
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageNoDataDecided,
		Direction: observability.DirectionInternal, Result: observability.ResultSuccess,
		NoDataCensus: &observability.NoDataCensusFacts{Hop: observability.NoDataHopDue, Plans: 0},
	})

	if _, reported := recorderCounterSeries(t, recorder, "bkmonitor_alarmd_worker_no_data_plans_seen_total"); !reported {
		t.Fatal("no census series after a Slot that found no no-data Plan; absent and zero then mean the " +
			"same thing, which is the reading this counter exists to split")
	}
}

func recorderCounterSeries(t *testing.T, recorder *Recorder, name string) (float64, bool) {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, series := range family.GetMetric() {
			return series.GetCounter().GetValue(), true
		}
	}
	return 0, false
}

func recorderCounterValue(t *testing.T, recorder *Recorder, name string) float64 {
	t.Helper()
	value, reported := recorderCounterSeries(t, recorder, name)
	if !reported {
		t.Fatalf("no series named %s", name)
	}
	return value
}

// noDataSlotSeriesFrom reads the outcome family out of a recorder's registry.
func noDataSlotSeriesFrom(t *testing.T, recorder *Recorder) map[string]float64 {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	read := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_worker_no_data_slot_plans_total" {
			continue
		}
		for _, series := range family.GetMetric() {
			for _, pair := range series.GetLabel() {
				if pair.GetName() == "outcome" {
					read[pair.GetValue()] = series.GetCounter().GetValue()
				}
			}
		}
	}
	return read
}

// Every hop reports into one family, and each label exists from startup.
//
// The family is read in order -- published, assembled, frozen, due -- and the
// first hop reading zero while the one before it does not is where the Plans
// stop existing. That reading needs every label present before anything
// happens: a hop that has reported nothing and a hop that reported zero are
// exactly the two the family exists to tell apart, and three releases were
// spent on the version of that question the call graph could not answer.
func TestEveryHopHasALabelBeforeAnythingHappens(t *testing.T) {
	recorder := NewRecorder(BuildInfo{Version: "0.2.9999", Commit: "0123456789abcdef", SchemaVersion: "v3"})
	read := hopSeries(t, recorder)
	if len(read) != len(observability.NoDataHops) {
		t.Fatalf("hops = %+v, want one series per hop (%d)", read, len(observability.NoDataHops))
	}
	for _, hop := range observability.NoDataHops {
		if value, present := read[hop]; !present || value != 0 {
			t.Fatalf("hop %q = %v (present=%t), want a reported zero", hop, value, present)
		}
	}
}

// A hop counts into its own label, from whichever side reported it.
//
// The leader reports the published hop and the workers report the rest, so the
// dispatch cannot be keyed on a component and stage without splitting the table
// the reader needs whole.
func TestEachHopCountsUnderItsOwnLabel(t *testing.T) {
	recorder := NewRecorder(BuildInfo{Version: "0.2.9999", Commit: "0123456789abcdef", SchemaVersion: "v3"})
	ctx := context.Background()
	recorder.Observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageSnapshotRefreshed,
		Result:       observability.ResultSuccess,
		NoDataCensus: &observability.NoDataCensusFacts{Hop: observability.NoDataHopPublished, Plans: 90},
	})
	recorder.Observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageSnapshotRefreshed,
		Result:       observability.ResultSuccess,
		NoDataCensus: &observability.NoDataCensusFacts{Hop: observability.NoDataHopAssembled, Plans: 12},
	})
	recorder.Observe(ctx, observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageNoDataDecided,
		Result:       observability.ResultSuccess,
		NoDataCensus: &observability.NoDataCensusFacts{Hop: observability.NoDataHopDue, Plans: 3},
	})

	read := hopSeries(t, recorder)
	for hop, want := range map[string]float64{
		observability.NoDataHopPublished: 90,
		observability.NoDataHopAssembled: 12,
		observability.NoDataHopFrozen:    0,
		observability.NoDataHopDue:       3,
	} {
		if read[hop] != want {
			t.Fatalf("hop %q = %v, want %v; hops = %+v", hop, read[hop], want, read)
		}
	}
	// And the standalone counter is the due hop, from the same emission, so
	// the two cannot disagree about it.
	if got := recorderCounterValue(t, recorder, "bkmonitor_alarmd_worker_no_data_plans_seen_total"); got != 3 {
		t.Fatalf("worker_no_data_plans_seen_total = %v, want the due hop's 3", got)
	}
}

func hopSeries(t *testing.T, recorder *Recorder) map[string]float64 {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	read := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_no_data_plans_by_hop_total" {
			continue
		}
		for _, series := range family.GetMetric() {
			for _, pair := range series.GetLabel() {
				if pair.GetName() == "hop" {
					read[pair.GetValue()] = series.GetCounter().GetValue()
				}
			}
		}
	}
	return read
}

// The Segment freshness states each have a label from startup, and each counts
// under its own.
//
// stale is expected to stay at zero on a converged fleet, which is exactly why
// the label has to exist before anything happens: the reading is "this number
// never moves", and a number that is absent instead of zero cannot be read that
// way. unknown is separate from current on purpose -- "could not check" and
// "checked and it is current" are the two a reader must not confuse.
func TestEverySegmentContentStateHasALabelAndCountsUnderIt(t *testing.T) {
	recorder := NewRecorder(BuildInfo{Version: "0.2.9999", Commit: "0123456789abcdef", SchemaVersion: "v3"})
	read := segmentContentSeries(t, recorder)
	if len(read) != len(controlplane.SegmentContentStates) {
		t.Fatalf("states = %+v, want one series per state (%d)", read, len(controlplane.SegmentContentStates))
	}
	for _, state := range controlplane.SegmentContentStates {
		if value, present := read[state]; !present || value != 0 {
			t.Fatalf("state %q = %v (present=%t), want a reported zero", state, value, present)
		}
	}

	ctx := context.Background()
	for _, state := range []string{
		controlplane.SegmentContentCurrent, controlplane.SegmentContentCurrent, controlplane.SegmentContentStale,
	} {
		recorder.Observe(ctx, observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageFrozenPlanGeneration,
			Result:         observability.ResultSuccess,
			SegmentContent: &observability.SegmentContentFacts{State: state},
		})
	}

	read = segmentContentSeries(t, recorder)
	if read[controlplane.SegmentContentCurrent] != 2 || read[controlplane.SegmentContentStale] != 1 {
		t.Fatalf("states = %+v, want two current and one stale", read)
	}
	if read[controlplane.SegmentContentUnknown] != 0 {
		t.Fatalf("unknown = %v, want a reported zero; a comparison that was made must not land there",
			read[controlplane.SegmentContentUnknown])
	}
}

func segmentContentSeries(t *testing.T, recorder *Recorder) map[string]float64 {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	read := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_segment_content_freshness_total" {
			continue
		}
		for _, series := range family.GetMetric() {
			for _, pair := range series.GetLabel() {
				if pair.GetName() == "state" {
					read[pair.GetValue()] = series.GetCounter().GetValue()
				}
			}
		}
	}
	return read
}

// Every cutover reason has a label from startup, and a failure counts under its
// own.
//
// other must stay at zero and the rest are expected to as well, which is
// precisely why every label has to exist before anything happens: the reading
// is "none of these ever move", and a number that is absent rather than zero
// cannot be read that way. A cutover failing every round with no named reason
// is how a fleet stopped picking up published content for eleven hours.
func TestEveryCutoverReasonHasALabelAndAFailureCountsUnderIts(t *testing.T) {
	recorder := NewRecorder(BuildInfo{Version: "0.2.9999", Commit: "0123456789abcdef", SchemaVersion: "v3"})
	read := cutoverSeries(t, recorder)
	for _, reason := range controlplane.CutoverReasons {
		if value, present := read["failure|"+reason]; !present || value != 0 {
			t.Fatalf("failure/%s = %v (present=%t), want a reported zero; series=%+v",
				reason, value, present, read)
		}
	}
	if value, present := read["success|"]; !present || value != 0 {
		t.Fatalf("success = %v (present=%t), want a reported zero", value, present)
	}

	ctx := context.Background()
	recorder.Observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageScheduleCutover,
		Result: observability.ResultFailed,
		ScheduleCutover: &observability.ScheduleCutoverFacts{
			Result: "failure", Reason: controlplane.CutoverReasonActivationRecordMissing,
			QueryGroup: "qg-a",
		},
	})
	recorder.Observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageScheduleCutover,
		Result:          observability.ResultSuccess,
		ScheduleCutover: &observability.ScheduleCutoverFacts{Result: "success"},
	})

	read = cutoverSeries(t, recorder)
	if read["failure|"+controlplane.CutoverReasonActivationRecordMissing] != 1 {
		t.Fatalf("the named failure did not count under its reason; series=%+v", read)
	}
	if read["success|"] != 1 {
		t.Fatalf("the success did not count; series=%+v", read)
	}
	if read["failure|"+controlplane.CutoverReasonOther] != 0 {
		t.Fatalf("a named failure landed in other; series=%+v", read)
	}
}

func cutoverSeries(t *testing.T, recorder *Recorder) map[string]float64 {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	read := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_schedule_cutover_total" {
			continue
		}
		for _, series := range family.GetMetric() {
			result, reason := "", ""
			for _, pair := range series.GetLabel() {
				switch pair.GetName() {
				case "result":
					result = pair.GetValue()
				case "reason":
					reason = pair.GetValue()
				}
			}
			read[result+"|"+reason] = series.GetCounter().GetValue()
		}
	}
	return read
}
