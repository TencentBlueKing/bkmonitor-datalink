// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// noDataCompiled is the fixture plan with no-data detection on, compiled the
// same way every other plan in these tests is.
func noDataCompiled(t *testing.T, continuous uint32) *strategy.CompiledPlan {
	t.Helper()
	return compiledWindowWithNoData(t, 1, 1, &contract.NoDataConfigV1{Continuous: continuous, Level: 2}, false)
}

// A synthetic no-data series is judged against the no-data level and only that.
//
// The completeness rule does not move: the inputs still have to cover the level
// set exactly. What moves is which set, and it moves because of what kind of
// series this is - not because of which inputs happened to arrive. A series
// with one absence point has no data for the item's declared levels and never
// will, so judging it against them would report every one of them unavailable
// every round.
func TestANoDataSeriesIsJudgedAgainstTheNoDataLevelOnly(t *testing.T) {
	plan := noDataCompiled(t, 1)
	level := plan.NoDataLevel()
	if level == nil {
		t.Fatal("fixture: the plan did not compile a no-data level")
	}

	request := noDataRequestFixture(t, plan, 1)
	result, err := newEvaluator(t).Evaluate(context.Background(), request)
	if err != nil {
		t.Fatalf("Evaluate() on a no-data series: %v", err)
	}
	if len(result.Plans) != 1 {
		t.Fatalf("plans = %d, want one", len(result.Plans))
	}
	outcomes := result.Plans[0].LevelOutcomes
	if len(outcomes) != 1 {
		t.Fatalf("level outcomes = %+v, want exactly the no-data level", outcomes)
	}
	if outcomes[0].LevelID != level.Definition().LevelID {
		t.Fatalf("outcome level = %d, want the configured no-data level %d",
			outcomes[0].LevelID, level.Definition().LevelID)
	}
	// The declared level is not among them, which is the whole point: it has no
	// data in this series and reporting it unavailable every round would be a
	// permanent false signal on a strategy that is working.
	for _, outcome := range outcomes {
		if outcome.LevelID == 5 {
			t.Fatalf("the declared level was judged from a synthetic series: %+v", outcome)
		}
	}
}

// A synthetic series for a Plan that does not detect no-data is refused.
//
// The worker builds these from the Plan's own configuration, so reaching here
// means the two disagree. Judging it anyway would mean either detecting absence
// a strategy never asked for, or picking a level out of the declared ones and
// feeding it an answer instead of a measurement.
func TestANoDataSeriesIsRefusedForAPlanThatDoesNotDetectNoData(t *testing.T) {
	plan := compiledWindow(t, 1, 1)
	if plan.NoDataLevel() != nil {
		t.Fatal("fixture: this plan is meant to have no no-data level")
	}
	request := noDataRequestFixture(t, plan, 1)
	_, err := newEvaluator(t).Evaluate(context.Background(), request)
	if err == nil {
		t.Fatal("a no-data series was evaluated against a Plan that does not detect no-data")
	}
	// The refusal has to be this one. Without the check, the evaluation carries
	// on with no Plan at all and fails a few steps later for a reason that says
	// nothing about what went wrong - and the test would still pass.
	if !strings.Contains(err.Error(), "no-data series for a Plan with no no-data level") {
		t.Fatalf("Evaluate() = %v, want it refused for having no no-data level", err)
	}
}

// A series whose kind this build does not know is refused rather than read as a
// real one. The kinds are a closed set decided where the input is built, so an
// unknown one means the two ends disagree about what is being evaluated, and
// guessing "real" would judge an absence answer against a threshold.
func TestASeriesOfAnUnknownKindIsRefused(t *testing.T) {
	request := requestFixture(t, json.RawMessage(`60`), nil)
	request.Inputs[0].Kind = execution.SeriesKind("SOMETHING_ELSE")
	_, err := newEvaluator(t).Evaluate(context.Background(), request)
	if err == nil {
		t.Fatal("a series of an unknown kind was evaluated as a real one")
	}
	if !strings.Contains(err.Error(), "unknown series kind") {
		t.Fatalf("Evaluate() = %v, want it refused for the kind", err)
	}
}

// The state a synthetic series writes carries one level, the configured one.
//
// It shares that number with whatever the strategy declared, and that is not a
// collision: the runtime state is keyed by series as well as Plan, and the
// synthetic series has its own identity because its dimensions carry the
// no-data tag. Writing the declared levels here instead would put levels in the
// record that nothing ever evaluates.
func TestTheStateANoDataSeriesWritesCarriesOnlyItsOwnLevel(t *testing.T) {
	plan := noDataCompiled(t, 1)
	request := noDataRequestFixture(t, plan, 1)
	result, err := newEvaluator(t).Evaluate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	states := result.Plans[0].StateResults
	if len(states) != 1 {
		t.Fatalf("state results = %d, want one for the one series", len(states))
	}
	mutation := states[0].Mutation
	if len(mutation.Levels) != 1 {
		t.Fatalf("state levels = %+v, want only the no-data level", mutation.Levels)
	}
	if mutation.Levels[0].LevelID != plan.NoDataLevel().Definition().LevelID {
		t.Fatalf("state level = %d, want the configured no-data level %d",
			mutation.Levels[0].LevelID, plan.NoDataLevel().Definition().LevelID)
	}
	if mutation.Identity.SeriesIdentityDigest != request.Inputs[0].SeriesIdentity {
		t.Fatalf("state series = %q, want the synthetic series' own identity %q",
			mutation.Identity.SeriesIdentityDigest, request.Inputs[0].SeriesIdentity)
	}
}

// A no-data alert opens and closes on the one path every other alert uses.
//
// This is the reason the synthetic series goes through this evaluator at all.
// Evaluating it somewhere else would have meant a second recovery gate, and the
// failure that follows from forgetting one is an alert that opens and never
// closes - which on a page full of open alerts is the hardest kind to notice,
// because it looks like the thing it is reporting never got better.
func TestANoDataAlertOpensAndRecoversOnTheOrdinaryPath(t *testing.T) {
	plan := noDataCompiled(t, 1)
	evaluator := newEvaluator(t)
	ctx := context.Background()

	absent := noDataRequestFixture(t, plan, 1)
	opened, err := evaluator.Evaluate(ctx, absent)
	if err != nil {
		t.Fatal(err)
	}
	if got := noDataEventKinds(opened); len(got) != 1 || got[0] != contract.TriggerEventAbnormal {
		t.Fatalf("an absent round produced %v, want one %s", got, contract.TriggerEventAbnormal)
	}

	// The same series reporting again. The absence answer is zero, which is not
	// an anomaly, and one window of misses is what the recovery plan asks for.
	present := noDataRequestFixtureAt(t, plan, 0, 160)
	present.State.Items[0] = openedStateFrom(t, opened, present.State.Items[0])
	recovered, err := evaluator.Evaluate(ctx, present)
	if err != nil {
		t.Fatal(err)
	}
	if got := noDataEventKinds(recovered); len(got) != 1 || got[0] != contract.TriggerEventRecovery {
		t.Fatalf("a round in which the group reported produced %v, want one %s",
			got, contract.TriggerEventRecovery)
	}
}

// noDataEventKinds is every event kind the one Plan result carries.
func noDataEventKinds(result execution.EvaluationResult) []string {
	kinds := make([]string, 0, 1)
	for _, plan := range result.Plans {
		for _, state := range plan.StateResults {
			for _, event := range state.Events {
				kinds = append(kinds, event.EventKind)
			}
		}
	}
	return kinds
}

// openedStateFrom carries the state the opening round wrote into the next
// round's view, so the second evaluation sees the window the first one left.
func openedStateFrom(
	t *testing.T, opened execution.EvaluationResult, view execution.RuntimeStateView,
) execution.RuntimeStateView {
	t.Helper()
	if len(opened.Plans) != 1 || len(opened.Plans[0].StateResults) != 1 {
		t.Fatalf("fixture: the opening round wrote %+v", opened.Plans)
	}
	mutation := opened.Plans[0].StateResults[0].Mutation
	view.Status = execution.StateFoundReady
	view.BlobRevision = 1
	view.VersionComparison = execution.ApplyVersionPersistedOlder
	view.History = append([]execution.StateHistoryPoint(nil), mutation.Points...)
	view.Levels = make([]execution.RuntimeLevelStateView, len(mutation.Levels))
	for index, level := range mutation.Levels {
		view.Levels[index] = execution.RuntimeLevelStateView{
			LevelID:                 level.LevelID,
			LevelStateCompatibility: level.LevelStateCompatibility,
			WarmupRequirementRef:    level.WarmupRequirementRef,
			HistoryCompleteness:     execution.HistoryFull,
			LastProcessedEventTime:  level.LastProcessedEventTime,
		}
	}
	return view
}

// noDataRequestFixture builds one synthetic absence point for the no-data level.
func noDataRequestFixture(t *testing.T, plan *strategy.CompiledPlan, value int) execution.EvaluationRequest {
	t.Helper()
	return noDataRequestFixtureAt(t, plan, value, 100)
}

// noDataRequestFixtureAt is the same at a chosen period, so a test can send a
// second round. A round reusing the first round's record is refused as
// disagreeing with the fact already stored for it, which is correct and is not
// what these tests are about.
func noDataRequestFixtureAt(
	t *testing.T, plan *strategy.CompiledPlan, value int, sourceTime int64,
) execution.EvaluationRequest {
	t.Helper()
	id := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	due := execution.DuePlan{Identity: id, CompiledPlan: plan, StateGeneration: "state-v1", StateApplyEpoch: 1,
		ScheduleRevision: "plan-schedule", CompletionDeadlineUnixMilli: 200000}
	levelID := uint32(2)
	if level := plan.NoDataLevel(); level != nil {
		levelID = level.Definition().LevelID
	}
	consumer := execution.ConsumerRef{Plan: id, LevelID: levelID, HasLevel: true}
	requirement := execution.DataRequirement{
		RequirementID: "main", DatasetName: "main", Role: execution.InputRolePrimary, LogicalQueryRef: "q",
		RelativeWindow: execution.RelativeQueryWindow{StartOffsetSeconds: -60, EndOffsetSeconds: 0, HalfOpen: true},
		StepMillis:     60000, AlignmentMillis: 60000, ResultWindowPolicy: execution.ResultWindowExactHalfOpen,
		ReadinessClass: execution.ReadinessEager, RequiredColumns: []string{strategy.NoDataValueField},
		Consumers: []execution.DataRequirementConsumer{{
			Consumer: consumer, ConsumerDeadlineUnixMilli: 200000, DownstreamExecutionReserveMilliSec: 1000,
		}},
	}
	digest, err := execution.DeriveDuePlanSetDigest([]execution.DuePlan{due}, []execution.DataRequirement{requirement})
	if err != nil {
		t.Fatal(err)
	}
	contractRef := execution.FrozenExecutionContractRef{
		Slot: execution.SlotIdentity{QueryGroup: "qg", EvaluationTime: execution.EvaluationTime(sourceTime)}, SnapshotRevision: "snapshot",
		QueryRevision: "query", ScheduleRevision: "schedule", ScheduleSegmentStart: execution.EvaluationTime(sourceTime), DuePlanSetDigest: digest,
	}
	records := []contract.CanonicalRecordV2{{
		RecordID: recordIDFor(sourceTime), SourceTime: sourceTime, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("e", 64)},
		Values:            map[string]json.RawMessage{strategy.NoDataValueField: json.RawMessage(itoa(value))},
		Dimensions:        map[string]json.RawMessage{contract.NoDataDimensionTag: json.RawMessage("true")},
		ReceivedTime:      sourceTime,
	}}
	series := execution.SeriesIdentityDigest(records[0].DimensionIdentity.Digest)
	dataset := execution.NewDataset(records)
	view, _ := execution.NewDatasetView(dataset, []uint32{0})
	binding := execution.NamedInputBinding{
		Consumer: consumer, RequirementID: "main", DatasetName: "main", Role: execution.InputRolePrimary,
		ProviderResult: "provider", QueryWindow: execution.QueryWindow{Start: sourceTime - 60, End: sourceTime},
		Dataset: dataset, View: view, Completeness: execution.CompletenessFull, DataState: execution.DataStateData,
		Disposition: execution.AccessAvailable, ImpactScope: execution.ImpactPlan,
		Provenance: execution.InputProvenance{PhysicalQuery: "physical", AttemptNo: 1},
	}
	provider := strategy.NewStaticScheduleProvider(strategy.TimezoneResolverFunc(
		func(context.Context, string, string, string) (*time.Location, error) { return time.UTC, nil }))
	requirementLevel := plan.Levels()[0]
	if level := plan.NoDataLevel(); level != nil {
		requirementLevel = *level
	}
	facts, err := provider.Resolve(context.Background(), []strategy.EffectiveTimeRequest{{
		TenantID: "tenant", BusinessID: "2", EvaluationTime: sourceTime,
		Requirement: requirementLevel.EffectiveTimeRequirement(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	stateView := execution.RuntimeStateView{
		Identity: execution.StateKeyIdentity{Plan: id, StateGeneration: "state-v1", SeriesIdentityDigest: series},
		Status:   execution.StateMissingWarming,
	}
	return execution.EvaluationRequest{
		Header: execution.InternalExecutionHeader{
			ExecutionID: "execution", Contract: contractRef, DuePlans: []execution.DuePlan{due},
			Requirements:            []execution.DataRequirement{requirement},
			EffectiveTimeFacts:      []execution.BoundEffectiveTimeFact{{Consumer: consumer, SeriesIdentity: series, Fact: facts[0]}},
			RequiredPhysicalQueries: []execution.PlannedPhysicalQueryRef{{Digest: "physical", QueryRevision: "query"}},
			DeadlineUnixMilli:       200000,
		},
		Inputs: []execution.SeriesEvaluationInputRequest{{
			Contract: contractRef, Consumer: consumer, SeriesIdentity: series,
			Kind: execution.SeriesKindNoData, RequirementIDs: []execution.RequirementID{"main"},
			Inputs: []execution.NamedInputBinding{binding},
		}},
		State: execution.StatePreflightResult{Items: []execution.RuntimeStateView{stateView}},
		Gaps: execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
			Identity: execution.PlanGapIdentity{Plan: id, StateGeneration: "state-v1"}, Status: execution.GapMissing,
		}}},
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	return "1"
}

// recordIDFor is a distinct sixty-four character record id per period.
func recordIDFor(sourceTime int64) string {
	id := strings.Repeat("d", 64)
	return id[:60] + fmt.Sprintf("%04d", sourceTime%10000)
}

// A point that lands between the positions its history is kept on reads as a
// window that is still filling, and nothing says otherwise.
//
// This is the reason the worker refuses an unaligned Slot before building any
// point, and the reason that refusal cannot be left to something downstream.
// The history summary does check alignment - and passes, because it measures
// the window from the point's own time, so an unaligned point is aligned with
// itself. What comes back is DECIDED_DEGRADED with HISTORY_WARMING: the same
// answer a genuinely warming window gives, every round, forever.
//
// The test states the silence rather than the guard, because the guard lives in
// another package and a comment there asserting this would be a claim with
// nothing behind it.
func TestAnOffGridPointIsIndistinguishableFromAWarmingWindow(t *testing.T) {
	plan := noDataCompiled(t, 3)
	evaluator := newEvaluator(t)
	ctx := context.Background()
	const base = 1788000000

	opened, err := evaluator.Evaluate(ctx, noDataRequestFixtureAt(t, plan, 1, base))
	if err != nil {
		t.Fatalf("the aligned round did not evaluate: %v", err)
	}

	// Forty-five seconds on, where a whole period is sixty.
	offGrid := noDataRequestFixtureAt(t, plan, 1, base+45)
	offGrid.State.Items[0] = openedStateFrom(t, opened, offGrid.State.Items[0])
	result, err := evaluator.Evaluate(ctx, offGrid)
	if err != nil {
		t.Fatalf("an off-grid round returned an error, so the worker's guard is no longer the only "+
			"thing standing between this and silence - revisit it: %v", err)
	}
	if len(result.Plans) != 1 || len(result.Plans[0].LevelOutcomes) != 1 {
		t.Fatalf("result = %+v, want one level outcome", result.Plans)
	}
	outcome := result.Plans[0].LevelOutcomes[0]
	if outcome.ReasonCode != execution.ReasonCode(contract.ReasonHistoryWarming) {
		t.Fatalf("an off-grid round reported %q; this test exists to record that it reports warming, "+
			"and if that changed the worker's guard should be reconsidered", outcome.ReasonCode)
	}
}
