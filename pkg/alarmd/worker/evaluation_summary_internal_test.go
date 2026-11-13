// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// What the Slot-wide readers read of a Slot's summary is what they read of
// its merged results: batches merged through the Slot's one merge point, the
// Plans interleaved and one Plan's disposition upgraded by a later batch,
// give the same present series, no-data end updates, events kept as
// identities, coverage, outcome mix and completion as walking the results.
// The results are still held beside the summary, so each reader's answer is
// taken from them the way it was before it moved.
func TestASlotsSummaryReadsWhatItsMergedResultsSay(t *testing.T) {
	planA := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "summary-a"}
	planB := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "summary-b"}
	stream := &streamedExecution{
		coordinator: &SlotExecutionCoordinator{budget: sideEffectTestBudget("")},
		header:      execution.InternalExecutionHeader{DuePlans: []execution.DuePlan{{Identity: planA}, {Identity: planB}}},
		// Only A's no-data end index was read, so only A's series are kept
		// as present.
		noDataEnds: map[execution.PlanIdentity]execution.NoDataEndLoaded{planA: {}},
	}
	defer stream.releaseProvisional()
	ref := execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "qg"}}
	digest := func(c string) execution.SeriesIdentityDigest {
		return execution.SeriesIdentityDigest(strings.Repeat(c, 64))
	}
	outcome := func(plan execution.PlanIdentity, series string, kind execution.LevelOutcomeKind) execution.LevelOutcome {
		return execution.LevelOutcome{Plan: plan, LevelID: 1, SeriesIdentityDigest: digest(series), Outcome: kind}
	}
	recovery := execution.EventWithoutMessage{EventKind: contract.TriggerEventRecovery, Format: contract.WireFormatPythonCompatible}
	end := func(fingerprint string) execution.NoDataEndUpdate {
		return execution.NoDataEndUpdate{Entry: execution.NoDataEndEntry{Fingerprint: fingerprint}, Judged: true}
	}
	batches := []execution.EvaluationResult{
		{Contract: ref, Result: observability.ResultSuccess, Plans: []execution.PlanEvaluationResult{{
			Plan: planA, Disposition: execution.PlanDecided,
			LevelOutcomes:   []execution.LevelOutcome{outcome(planA, "a", execution.LevelOutcomeNormal)},
			NoDataEnds:      []execution.NoDataEndUpdate{end("fa")},
			HistoryCoverage: execution.HistoryCoverage{Levels: 2, Short: 1, WorstValid: 3, WorstRequired: 5},
			StateResults:    []execution.StateEvaluation{{WithoutMessage: []execution.EventWithoutMessage{recovery}}},
		}}},
		{Contract: ref, Result: observability.ResultDegraded, Plans: []execution.PlanEvaluationResult{{
			Plan: planB, Disposition: execution.PlanDecided,
			LevelOutcomes:   []execution.LevelOutcome{outcome(planB, "c", execution.LevelOutcomeUnknown)},
			HistoryCoverage: execution.HistoryCoverage{Levels: 1},
		}, {
			Plan: planA, Disposition: execution.PlanUnavailable, ReasonCode: contract.ReasonQueryUnavailable,
			LevelOutcomes:   []execution.LevelOutcome{outcome(planA, "b", execution.LevelOutcomeUnknown)},
			NoDataEnds:      []execution.NoDataEndUpdate{end("fb")},
			HistoryCoverage: execution.HistoryCoverage{Levels: 1, Short: 1, WorstValid: 1, WorstRequired: 9},
			StateResults:    []execution.StateEvaluation{{WithoutMessage: []execution.EventWithoutMessage{recovery, recovery}}},
		}}},
		{Contract: ref, Result: observability.ResultSuccess, Plans: []execution.PlanEvaluationResult{{
			Plan: planA, Disposition: execution.PlanDecided,
			LevelOutcomes: []execution.LevelOutcome{outcome(planA, "a", execution.LevelOutcomeAbnormal)},
		}}},
	}
	for _, batch := range batches {
		if err := stream.mergeProvisional(context.Background(), batch, 0); err != nil {
			t.Fatal(err)
		}
	}
	evaluated := stream.evaluated
	if len(evaluated.Plans) != 2 || stream.summary.planCount() != 2 {
		t.Fatalf("results name %d Plans and the summary %d, want both A and B once", len(evaluated.Plans), stream.summary.planCount())
	}

	// The present series of the Plan whose index was read.
	present := map[execution.SeriesIdentityDigest]struct{}{}
	for _, plan := range evaluated.Plans {
		if plan.Plan == planA {
			for _, outcome := range plan.LevelOutcomes {
				present[outcome.SeriesIdentityDigest] = struct{}{}
			}
		}
	}
	if got := stream.noDataEndRound()[planA].present; !reflect.DeepEqual(got, present) || len(got) != 2 {
		t.Fatalf("present = %v, want A's two series %v", got, present)
	}
	if part, _ := stream.summary.planPart(planB); part.present != nil {
		t.Fatalf("B's series kept as present %v, and nothing reads B's index", part.present)
	}

	// The no-data end updates, every series' of each Plan.
	for _, plan := range evaluated.Plans {
		if got := updatesOf(&stream.summary, plan.Plan); !reflect.DeepEqual(got, plan.NoDataEnds) {
			t.Fatalf("%s updates = %+v, want %+v", plan.Plan.StrategyID, got, plan.NoDataEnds)
		}
	}
	if got := updatesOf(&stream.summary, planA); len(got) != 2 {
		t.Fatalf("A's updates = %+v, want both series'", got)
	}

	// The events kept as identities.
	var withoutMessage uint64
	for _, plan := range evaluated.Plans {
		for _, state := range plan.StateResults {
			withoutMessage += uint64(len(state.WithoutMessage))
		}
	}
	if got := stream.budgetUsage().EventsWithoutMessage; got != withoutMessage || got != 3 {
		t.Fatalf("events without message = %d, want %d across both batches", got, withoutMessage)
	}

	// The Slot's coverage.
	var coverage execution.HistoryCoverage
	for _, plan := range evaluated.Plans {
		coverage.Merge(plan.HistoryCoverage)
	}
	if got := stream.summary.coverage(); !reflect.DeepEqual(got, coverage) || got.Levels != 4 {
		t.Fatalf("coverage = %+v, want %+v", got, coverage)
	}

	// The outcome mix and the dispositions the query line names.
	outcomes := map[execution.LevelOutcomeKind]int{}
	var dispositions []execution.PlanDisposition
	for _, plan := range evaluated.Plans {
		dispositions = append(dispositions, plan.Disposition)
		for _, outcome := range plan.LevelOutcomes {
			outcomes[outcome.Outcome]++
		}
	}
	if !reflect.DeepEqual(stream.summary.levelOutcomes, outcomes) {
		t.Fatalf("outcome mix = %v, want %v", stream.summary.levelOutcomes, outcomes)
	}
	if got := stream.summary.completion.Dispositions(); !reflect.DeepEqual(got, dispositions) ||
		got[0] != execution.PlanUnavailable {
		t.Fatalf("dispositions = %v, want %v with A upgraded by its second batch", got, dispositions)
	}
	if got, want := unexplainedOutcomeFacts(observability.ResultDegraded, "", &stream.summary),
		"plans=2 dispositions=DECIDED,UNAVAILABLE levels=ABNORMAL:1,NORMAL:1,UNKNOWN:2"; got == nil || got.Detail != want {
		t.Fatalf("unexplained outcome = %+v, want detail %q", got, want)
	}

	// The completion.
	bindings := []execution.NamedInputBinding{{Consumer: execution.ConsumerRef{Plan: planA}, Role: execution.InputRolePrimary,
		Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Disposition: execution.AccessAvailable}}
	kind, attribution, err := execution.DeriveStreamingCompletionAttribution(stream.header, bindings, evaluated)
	if err != nil {
		t.Fatal(err)
	}
	foldedKind, folded, err := execution.DeriveStreamingFoldedCompletionAttribution(stream.header, bindings, stream.summary.completion)
	if err != nil || foldedKind != kind || !reflect.DeepEqual(folded, attribution) {
		t.Fatalf("folded completion = %s %+v %v, want %s %+v", foldedKind, folded, err, kind, attribution)
	}
	if kind != execution.CompletionUnavailable || attribution.Cause != execution.CausePlanUnavailable ||
		attribution.Reason != contract.ReasonQueryUnavailable {
		t.Fatalf("completion = %s %+v, want A unavailable for its query", kind, attribution)
	}
}

// A Slot replaced by its budget gap keeps nothing of the results it
// summarised: the completion, the row and the query line read the replacement
// Plan, as they read stream.evaluated, and not the series that will not be
// written.
func TestASlotReplacedByItsBudgetGapSummarisesOnlyTheReplacement(t *testing.T) {
	due := noDataWiredPlan(t)
	stream := noDataWiredStream(t, due, &emptyNoDataStore{})
	defer stream.releaseProvisional()
	stream.gaps = execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{Identity: due.GapIdentity(), Status: execution.GapMissing}}}
	recovery := execution.EventWithoutMessage{EventKind: contract.TriggerEventRecovery, Format: contract.WireFormatPythonCompatible}
	decided := execution.EvaluationResult{Contract: stream.header.Contract, Result: observability.ResultSuccess,
		Plans: []execution.PlanEvaluationResult{{Plan: due.Identity, Disposition: execution.PlanDecided,
			LevelOutcomes: []execution.LevelOutcome{{Plan: due.Identity, LevelID: 1, Outcome: execution.LevelOutcomeAbnormal}},
			StateResults:  []execution.StateEvaluation{{WithoutMessage: []execution.EventWithoutMessage{recovery}}}}}}
	if err := stream.mergeProvisional(context.Background(), decided, 0); err != nil {
		t.Fatal(err)
	}
	if stream.summary.withoutMessage != 1 {
		t.Fatalf("events without message before the replacement = %d, want 1", stream.summary.withoutMessage)
	}
	if err := stream.completeBeyondSlotBudget(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := stream.summary.completion.Dispositions(); stream.summary.planCount() != len(stream.evaluated.Plans) ||
		!reflect.DeepEqual(got, []execution.PlanDisposition{execution.PlanUnavailable}) {
		t.Fatalf("summary Plans = %v, want the one replacement Plan the results hold", got)
	}
	if stream.summary.withoutMessage != 0 || len(stream.summary.levelOutcomes) != 0 {
		t.Fatalf("summary after the replacement = %+v, want nothing of the discarded results", stream.summary)
	}
}
