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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// absenceLinesOf runs one Plan's no-data round against the given memory and
// returns the absence lines the round emitted, with the round itself.
func absenceLinesOf(
	t *testing.T, horizon int64, groups []execution.NoDataGroupMemory, present int64,
) (noDataRound, []observability.NoDataAbsenceFacts, []observability.Observation) {
	t.Helper()
	plan := noDataWiredPlan(t)
	config := &contract.NoDataConfigV1{
		Continuous: 1, Level: 2, AggDimension: []string{"bk_target_ip", "bk_target_cloud_id"},
		TrackingHorizonSeconds: horizon,
	}
	if horizon > 0 {
		// The strategy's own rather than the platform's so that the line
		// cannot pass by carrying whichever word a reader would infer from
		// the number.
		config.TrackingHorizonSource = contract.NoDataHorizonSourceStrategy
	}
	plan.CompiledPlan = noDataPreflightPlan(t, "7", config)
	stream := noDataWiredStream(t, plan, &horizonNoDataStore{groups: groups, present: present})
	recorded := []observability.Observation{}
	stream.coordinator.ports.Observer = observability.ObserverFunc(
		func(_ context.Context, observation observability.Observation) {
			recorded = append(recorded, observation)
		})
	if err := stream.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	round, err := stream.noDataRoundFor(plan, nil, execution.CompletenessFull)
	if err != nil {
		t.Fatal(err)
	}
	stream.observeNoDataAbsence(context.Background(), plan, round)
	lines := []observability.NoDataAbsenceFacts{}
	for _, observation := range recorded {
		if observation.NoDataAbsence != nil {
			lines = append(lines, *observation.NoDataAbsence)
		}
	}
	return round, lines, recorded
}

// A round that judged says what it counted, on the round it counted it.
//
// The numbers on the line are the evaluation's own -- carried out of the
// decision, not recomputed by the emitter and not differenced against the
// last round's memory -- and the horizon on the line is the one the Plan
// carried into the decision. Two memories that differ only in how old their
// absences are give the line different expired counts; that is what shows
// the line reads the decision and not the shape of the memory.
func TestARoundThatJudgedSaysWhatItCountedAgainstWhichHorizon(t *testing.T) {
	const horizon = int64(600)
	evaluation := int64(noDataPreflightContract(t, []execution.DuePlan{noDataWiredPlan(t)}).Slot.EvaluationTime)
	key := func(ip string) string {
		return "bk_target_cloud_id=0,bk_target_ip=" + ip + "," + contract.NoDataDimensionTag + "=true"
	}
	// Two absences the horizon has outlived and one it has not, so expired and
	// absent are different numbers and neither is the number of groups.
	groups := []execution.NoDataGroupMemory{
		{GroupKey: key("192.0.2.1"), LastSeen: evaluation - horizon*4, FirstAbsent: evaluation - horizon*3},
		{GroupKey: key("192.0.2.2"), LastSeen: evaluation - horizon*4, FirstAbsent: evaluation - horizon*2},
		{GroupKey: key("192.0.2.3"), LastSeen: evaluation - horizon*4, FirstAbsent: evaluation - horizon/2},
	}
	round, lines, _ := absenceLinesOf(t, horizon, groups, evaluation-horizon*4)
	if round.outcome != nodata.OutcomeEvaluated {
		t.Fatalf("outcome = %q, want the round to have judged", round.outcome)
	}
	if len(lines) != 1 {
		t.Fatalf("emitted %d absence lines, want exactly one for the one Plan that judged", len(lines))
	}
	line := lines[0]
	if line.Outcome != string(nodata.OutcomeEvaluated) {
		t.Fatalf("line outcome = %q, want %q", line.Outcome, nodata.OutcomeEvaluated)
	}
	if line.HorizonSeconds != horizon {
		t.Fatalf("line horizon = %d, want the Plan's %d", line.HorizonSeconds, horizon)
	}
	if line.HorizonSource != string(contract.NoDataHorizonSourceStrategy) {
		t.Fatalf("line horizon source = %q, want the Plan's frozen %q: a reader of the line must not have to "+
			"compare the number against the platform's to know whose it is", line.HorizonSource, contract.NoDataHorizonSourceStrategy)
	}
	if line.Expired != 2 {
		t.Fatalf("line expired = %d, want the 2 absences the horizon outlived (facts %+v)", line.Expired, round.facts)
	}
	if line.Absent != 1 {
		t.Fatalf("line absent = %d, want the 1 absence still tracked (facts %+v)", line.Absent, round.facts)
	}
	if line.Expected != 3 || line.Present != 0 {
		t.Fatalf("line expected/present = %d/%d, want 3/0: the roster is the three remembered groups and nothing arrived",
			line.Expected, line.Present)
	}
	if line.RosterSource != string(nodata.RosterHistory) {
		t.Fatalf("line roster source = %q, want %q", line.RosterSource, nodata.RosterHistory)
	}
	// The one absence still tracked is horizon/2 = 300 s old: under an hour.
	// The two the horizon stopped are in no bucket.
	if line.AbsentAges != (observability.NoDataAbsentAges{UnderHour: 1}) {
		t.Fatalf("line ages = %+v, want the one tracked absence under an hour and nothing for the expired", line.AbsentAges)
	}
	fromRound := observability.NoDataAbsenceFacts{
		Outcome: string(round.outcome), HorizonSeconds: round.horizon, HorizonSource: round.horizonSource,
		RosterSource: string(round.facts.RosterSource),
		Expected:     round.facts.Expected, Present: round.facts.Present, Absent: round.facts.Absent,
		Unavailable: round.facts.Unavailable, Dropped: round.facts.Dropped,
		Expired: round.facts.Expired, Suppressed: round.facts.Suppressed,
		AbsentAges: observability.NoDataAbsentAges{
			ThisRound: round.facts.AbsentAges.ThisRound, UnderHour: round.facts.AbsentAges.UnderHour,
			UnderDay: round.facts.AbsentAges.UnderDay, DayOrMore: round.facts.AbsentAges.DayOrMore,
		},
	}
	if line != fromRound {
		t.Fatalf("line %+v does not carry the round's own facts %+v", line, round.facts)
	}

	// The control: the same memory, no horizon. Nothing expires, all three are
	// still tracked, and the line says so with a zero horizon -- the reading a
	// deployment that has not switched the horizon on is meant to get.
	_, control, _ := absenceLinesOf(t, 0, groups, evaluation-horizon*4)
	if len(control) != 1 {
		t.Fatalf("control emitted %d absence lines, want one", len(control))
	}
	if control[0].HorizonSeconds != 0 || control[0].HorizonSource != "" || control[0].Expired != 0 || control[0].Absent != 3 {
		t.Fatalf("control line = %+v, want horizon 0 from nowhere, expired 0, absent 3", control[0])
	}
	// Without a horizon all three are tracked, 1800 s, 1200 s and 300 s old:
	// all under an hour, and the buckets sum to Absent.
	if control[0].AbsentAges != (observability.NoDataAbsentAges{UnderHour: 3}) {
		t.Fatalf("control ages = %+v, want all three under an hour", control[0].AbsentAges)
	}
}

// A memory that already carries a stopped absence reports it as suppressed,
// not as expired again and not as absent: the standing size of what the
// horizon is holding down is its own number.
func TestAnAbsenceAlreadyStoppedIsReportedAsSuppressedNotExpired(t *testing.T) {
	const horizon = int64(600)
	evaluation := int64(noDataPreflightContract(t, []execution.DuePlan{noDataWiredPlan(t)}).Slot.EvaluationTime)
	key := func(ip string) string {
		return "bk_target_cloud_id=0,bk_target_ip=" + ip + "," + contract.NoDataDimensionTag + "=true"
	}
	// A history roster forgets an expired group, so a stopped absence a round
	// can meet again is the mark a target roster leaves; the fixture store
	// declares HISTORY, under which a still-remembered SuppressedAt on a
	// group is met in the roster loop and counted as suppressed.
	groups := []execution.NoDataGroupMemory{
		{GroupKey: key("192.0.2.1"), LastSeen: evaluation - horizon*4, FirstAbsent: evaluation - horizon*3,
			SuppressedAt: evaluation - horizon*2},
		{GroupKey: key("192.0.2.2"), LastSeen: evaluation - horizon*4, FirstAbsent: evaluation - horizon*3,
			SuppressedAt: evaluation - horizon*2},
		{GroupKey: key("192.0.2.3"), LastSeen: evaluation - horizon*4, FirstAbsent: evaluation - horizon/2},
	}
	round, lines, _ := absenceLinesOf(t, horizon, groups, evaluation-horizon*4)
	if round.outcome != nodata.OutcomeEvaluated {
		t.Fatalf("outcome = %q, want the round to have judged", round.outcome)
	}
	if len(lines) != 1 {
		t.Fatalf("emitted %d absence lines, want one", len(lines))
	}
	if lines[0].Suppressed != 2 || lines[0].Expired != 0 || lines[0].Absent != 1 {
		t.Fatalf("line = %+v, want suppressed 2, expired 0, absent 1", lines[0])
	}
}

// A round that did not judge says nothing on this line: the skipped outcomes
// have their partition line, and a line of zeros under them would read as a
// Plan with no groups.
func TestARoundThatDidNotJudgeEmitsNoAbsenceLine(t *testing.T) {
	recorded := []observability.Observation{}
	stream := &streamedExecution{coordinator: &SlotExecutionCoordinator{
		ports: Ports{Observer: observability.ObserverFunc(
			func(_ context.Context, observation observability.Observation) {
				recorded = append(recorded, observation)
			})},
	}}
	for _, outcome := range nodata.SlotOutcomes {
		if outcome == nodata.OutcomeEvaluated {
			continue
		}
		stream.observeNoDataAbsence(context.Background(), noDataWiredPlan(t), noDataRound{
			outcome: outcome, horizon: 600, facts: nodata.AbsenceFacts{Expected: 3, Unavailable: 3},
		})
	}
	if len(recorded) != 0 {
		t.Fatalf("a round that did not judge emitted %d observations: %+v", len(recorded), recorded)
	}
}

// The line comes out of the round's own pass, once for the Plan that judged
// and not for the Plan the budget skipped, and only when the Slot reports its
// outcomes: read through evaluateNoData and the report, the two steps the
// Slot runs, rather than the emitter, because a call site that was never
// wired reads the same as one that was under a test that calls the emitter
// itself. Before the report nothing is said: a judgement the Slot discards
// is not reported as made.
func TestTheAbsenceLineComesOutOfTheRoundsOwnPass(t *testing.T) {
	first := noDataWiredPlan(t)
	second := first
	second.Identity.StrategyID = "8"
	second.CompiledPlan = noDataPreflightPlan(t, "8", &contract.NoDataConfigV1{
		Continuous: 1, Level: 2, AggDimension: []string{"bk_target_ip", "bk_target_cloud_id"},
	})
	duePlans := []execution.DuePlan{first, second}
	recorded := []observability.Observation{}
	stream := &streamedExecution{
		coordinator: &SlotExecutionCoordinator{
			ports: Ports{NoData: &emptyNoDataStore{}, Hosts: SharedHostBusiness, State: failingStatePort{},
				Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
					recorded = append(recorded, observation)
				})},
			// One mutation for the whole Slot: the first Plan judges and its
			// series fits, the second is skipped by the budget.
			budget: ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxGapMutations: 10, MaxStateMutations: 1, MaxEvents: 1},
		},
		header: execution.InternalExecutionHeader{Contract: noDataPreflightContract(t, duePlans), DuePlans: duePlans},
	}
	if err := stream.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The batch fails on purpose - this fixture has no evaluator - and that is
	// after both Plans have been decided and their lines emitted.
	if err := stream.evaluateNoData(context.Background(), nil, 16); err == nil {
		t.Fatal("fixture: the batch was expected to fail, so its success means this read something else")
	}
	for _, observation := range recorded {
		if observation.NoDataAbsence != nil {
			t.Fatalf("an absence line came out before the Slot reported its outcomes: %+v", observation)
		}
	}
	stream.observeNoDataOutcomes(context.Background())
	lines := map[string]observability.NoDataAbsenceFacts{}
	for _, observation := range recorded {
		if observation.NoDataAbsence == nil {
			continue
		}
		if observation.Stage != observability.StageNoDataDecided {
			t.Fatalf("the absence line came out under stage %q, want %q", observation.Stage, observability.StageNoDataDecided)
		}
		if _, twice := lines[observation.Trace.StrategyID]; twice {
			t.Fatalf("strategy %s got two absence lines in one Slot", observation.Trace.StrategyID)
		}
		lines[observation.Trace.StrategyID] = *observation.NoDataAbsence
	}
	if len(lines) != 1 {
		t.Fatalf("absence lines by strategy = %+v, want exactly one, for the Plan that judged", lines)
	}
	line, judged := lines[first.Identity.StrategyID]
	if !judged {
		t.Fatalf("the line names %v, want the judging Plan %s", lines, first.Identity.StrategyID)
	}
	// A whole-item absence with nothing expected and nothing arriving: one
	// absent, which is the reading a fresh Plan on an empty store gives.
	if line.Outcome != string(nodata.OutcomeEvaluated) || line.Absent != 1 || line.Expected != 0 || line.Present != 0 {
		t.Fatalf("line = %+v, want EVALUATED with the whole-item absence counted once", line)
	}
}

// A Slot whose own output went past a per-Slot cap decided no Plan's no-data,
// whatever the rounds had filed: every no-data Plan lands on exactly one
// outcome, SKIPPED_SLOT_BUDGET where it had none and where it had filed
// EVALUATED; the census counts the Plans the Slot had even when the cap
// tripped before they were seen; the judging Plan's line is not reported and
// its memory is not written.
func TestASlotBeyondItsBudgetSettlesEveryNoDataPlanOnce(t *testing.T) {
	first := noDataWiredPlan(t)
	second := first
	second.Identity.StrategyID = "8"
	second.CompiledPlan = noDataPreflightPlan(t, "8", &contract.NoDataConfigV1{
		Continuous: 1, Level: 2, AggDimension: []string{"bk_target_ip", "bk_target_cloud_id"},
	})
	duePlans := []execution.DuePlan{first, second}
	for _, decidedFirst := range []bool{true, false} {
		recorded := []observability.Observation{}
		stream := &streamedExecution{
			coordinator: &SlotExecutionCoordinator{
				ports: Ports{NoData: &emptyNoDataStore{}, Hosts: SharedHostBusiness, State: failingStatePort{},
					Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
						recorded = append(recorded, observation)
					})},
				budget: ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxGapMutations: 10, MaxStateMutations: 1, MaxEvents: 1},
			},
			header: execution.InternalExecutionHeader{Contract: noDataPreflightContract(t, duePlans), DuePlans: duePlans},
		}
		if err := stream.loadNoDataMemory(context.Background()); err != nil {
			t.Fatal(err)
		}
		if decidedFirst {
			// The first Plan judges and files EVALUATED, the second is skipped by
			// the no-data budget; then the cap trips.
			_ = stream.evaluateNoData(context.Background(), nil, 16)
			// The guard on the fixture: the case is only read if the first Plan
			// really judged, or the correction below is never exercised.
			index, filed := stream.noDataOutcomeIndex[first.Key()]
			if len(stream.noDataOutcomes) != 2 || !filed || stream.noDataOutcomes[index] != nodata.OutcomeEvaluated {
				t.Fatalf("fixture: outcomes %v before the trip, want the first Plan EVALUATED and the second skipped", stream.noDataOutcomes)
			}
		}
		// Through the replacement itself, which is where the settling is wired.
		// It settles the no-data side first; this fixture loads no gap
		// markers, so the replacement gaps after it fail, and that is not
		// what is read here.
		_ = stream.completeBeyondSlotBudget(context.Background())
		stream.observeNoDataOutcomes(context.Background())
		// The discarded EVALUATED counts as a skip: the streak the judgement
		// reset starts again at this round.
		if rounds := stream.coordinator.noDataSkips.rounds[first.Key()]; rounds != 1 {
			t.Fatalf("decided first=%v: first Plan's streak = %d, want 1", decidedFirst, rounds)
		}
		if stream.noDataMutations != nil {
			t.Fatalf("decided first=%v: memory mutations %+v survived a replaced Slot", decidedFirst, stream.noDataMutations)
		}
		census, partition := 0, map[string]int{}
		for _, observation := range recorded {
			switch {
			case observation.NoDataAbsence != nil:
				t.Fatalf("decided first=%v: a replaced Slot reported a judgement: %+v", decidedFirst, observation.NoDataAbsence)
			case observation.NoDataCensus != nil:
				census = observation.NoDataCensus.Plans
			case observation.NoDataSlot != nil:
				partition[observation.NoDataSlot.Outcome] += observation.NoDataSlot.Plans
			}
		}
		if census != 2 || len(partition) != 1 || partition[string(nodata.OutcomeSkippedSlotBudget)] != 2 {
			t.Fatalf("decided first=%v: census=%d partition=%v, want both Plans and both on SKIPPED_SLOT_BUDGET", decidedFirst, census, partition)
		}
	}
}

// A Query Group whose Slot goes past its cap every round has stopped
// detecting no-data, and is reported as a stall on its third round like any
// other persistent skip. Each round its Plan judges first and files
// EVALUATED, and the cap then turns that into SKIPPED_SLOT_BUDGET: the
// streak follows the outcome the Slot settles on, not the one filed first,
// or every round would reset the streak it then counts as one.
func TestASlotBeyondItsBudgetEveryRoundIsReportedAsAStall(t *testing.T) {
	first := noDataWiredPlan(t)
	duePlans := []execution.DuePlan{first}
	recorded := []observability.Observation{}
	coordinator := &SlotExecutionCoordinator{
		ports: Ports{NoData: &emptyNoDataStore{}, Hosts: SharedHostBusiness, State: failingStatePort{},
			Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				recorded = append(recorded, observation)
			})},
		budget: ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxGapMutations: 10, MaxStateMutations: 1, MaxEvents: 1},
	}
	contract := noDataPreflightContract(t, duePlans)
	for round := 1; round <= 4; round++ {
		stream := &streamedExecution{coordinator: coordinator,
			header: execution.InternalExecutionHeader{Contract: contract, DuePlans: duePlans}}
		stream.request.Contract = contract
		stream.request.Contract.Slot.EvaluationTime += execution.EvaluationTime(60 * round)
		if err := stream.loadNoDataMemory(context.Background()); err != nil {
			t.Fatal(err)
		}
		_ = stream.evaluateNoData(context.Background(), nil, 16)
		if index, filed := stream.noDataOutcomeIndex[first.Key()]; !filed || stream.noDataOutcomes[index] != nodata.OutcomeEvaluated {
			t.Fatalf("fixture: round %d filed %v before the cap, want EVALUATED", round, stream.noDataOutcomes)
		}
		mark := len(recorded)
		_ = stream.completeBeyondSlotBudget(context.Background())
		stream.observeNoDataOutcomes(context.Background())
		var stalls []string
		for _, observation := range recorded[mark:] {
			if observation.NoDataStall != nil {
				stalls = append(stalls, observation.NoDataStall.Outcome)
			}
		}
		want := []string(nil)
		if round == 3 {
			want = []string{"SKIPPED_SLOT_BUDGET"}
		}
		if !reflect.DeepEqual(stalls, want) {
			t.Fatalf("round %d reported stalls %v, want %v: three rounds past the cap are a stall, reported once", round, stalls, want)
		}
	}
}
