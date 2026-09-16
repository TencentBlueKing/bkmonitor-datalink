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
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func noDataWiredPlan(t *testing.T) execution.DuePlan {
	t.Helper()
	return execution.DuePlan{
		Identity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"},
		CompiledPlan: noDataPreflightPlan(t, "7", &contract.NoDataConfigV1{
			Continuous: 1, Level: 2, AggDimension: []string{"bk_target_ip", "bk_target_cloud_id"},
		}),
		StateGeneration: "state-v1", StateApplyEpoch: 1, ScheduleRevision: "plan-schedule-v1",
	}
}

func noDataWiredStream(t *testing.T, due execution.DuePlan, store execution.PlanNoDataStore) *streamedExecution {
	t.Helper()
	duePlans := []execution.DuePlan{due}
	return &streamedExecution{
		coordinator: &SlotExecutionCoordinator{
			ports:  Ports{NoData: store, Hosts: SharedHostBusiness},
			budget: ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxGapMutations: 10},
		},
		header: execution.InternalExecutionHeader{
			Contract: noDataPreflightContract(t, duePlans), DuePlans: duePlans,
		},
	}
}

// A round with no target and nothing reported judges the item as a whole, and
// that verdict becomes a series the ordinary evaluation can read: one record,
// one primary binding, and the kind that says which level it is judged against.
func TestANoDataRoundProducesASeriesTheEvaluationCanRead(t *testing.T) {
	due := noDataWiredPlan(t)
	stream := noDataWiredStream(t, due, &emptyNoDataStore{})
	if err := stream.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}

	round, err := stream.noDataRoundFor(due, nil, execution.CompletenessFull)
	if err != nil {
		t.Fatal(err)
	}
	if round.outcome != nodata.OutcomeEvaluated {
		t.Fatalf("outcome = %q, want the round to have judged", round.outcome)
	}
	if len(round.series) != 1 {
		t.Fatalf("series = %d, want the whole item judged once", len(round.series))
	}

	entry := round.series[0]
	if entry.kind() != execution.SeriesKindNoData {
		t.Fatalf("kind = %q, want %q; without it the series is judged against the declared levels",
			entry.kind(), execution.SeriesKindNoData)
	}
	// The Plan it carries is the no-data view, so everything downstream that
	// asks for levels gets the one this series is judged against -- and that is
	// the level the no-data configuration names, not the strategy's own.
	//
	// Stated as the number rather than as "the same as NoDataLevel()", which
	// would move with whatever NoDataLevel returned and agree with itself while
	// being wrong. This fixture configures no-data at level 2 and detects
	// thresholds at level 5, so the two answers are different numbers: the
	// level is the last segment of the anomaly_id, and taking the strategy's
	// would give every no-data alert a deduplication key that no group on the
	// other side shares.
	const noDataConfiguredLevel = uint32(2)
	if levels := entry.due.CompiledPlan.Levels(); len(levels) != 1 ||
		levels[0].Definition().LevelID != noDataConfiguredLevel {
		t.Fatalf("the series carries levels %+v, want only the configured no-data level %d", levels, noDataConfiguredLevel)
	}
	if entry.inputs[0].Inputs[0].Consumer.LevelID != noDataConfiguredLevel {
		t.Fatalf("the binding names level %d, want the configured no-data level %d",
			entry.inputs[0].Inputs[0].Consumer.LevelID, noDataConfiguredLevel)
	}
	if entry.item.Identity.SeriesIdentityDigest != entry.series {
		t.Fatalf("state key series %q does not match the series %q",
			entry.item.Identity.SeriesIdentityDigest, entry.series)
	}

	binding := entry.inputs[0].Inputs[0]
	if binding.Role != execution.InputRolePrimary || binding.View == nil {
		t.Fatalf("binding = %+v, want one primary binding with a view", binding)
	}
	record, ok := binding.View.Record(0)
	if !ok {
		t.Fatal("the synthetic binding carries no record")
	}
	// The value is the answer: one for absent. The tag is a dimension, which is
	// what keeps this series' identity away from the item's real ones.
	if got := string(record.Values()[strategy.NoDataValueField]); got != "1" {
		t.Fatalf("synthetic value = %s, want 1 for an absent group", got)
	}
	if _, tagged := record.Dimensions()[contract.NoDataDimensionTag]; !tagged {
		t.Fatalf("synthetic dimensions = %v, want the no-data tag", record.Dimensions())
	}
	// And the point carries how long the group has been silent. The alert text
	// states it and the output layer has no other way to know: the count is
	// decided here, where the absence is, and the values are the only channel
	// a synthetic point has to the converter.
	if got := string(record.Values()[contract.NoDataPeriodFactField]); got != "1" {
		t.Fatalf("synthetic period count = %q, want 1 for a group absent since this round. Without it "+
			"every no-data alert says one period however long the silence has lasted", got)
	}
	// Exactly one period behind, not merely behind: the point is the period this
	// Slot decided, and the backend's anomaly_id is built from that timestamp -
	// off by a second and no Python-written record matches it.
	period := int64(due.CompiledPlan.EvaluationSemantics().EvaluationInterval)
	if want := int64(stream.header.Contract.Slot.EvaluationTime) - period; record.SourceTime() != want {
		t.Fatalf("source time = %d, want %d: the Slot's time less one period",
			record.SourceTime(), want)
	}
}

// The memory a round produces is stored, and stored once.
func TestANoDataRoundStoresWhatItRemembered(t *testing.T) {
	due := noDataWiredPlan(t)
	store := &emptyNoDataStore{}
	stream := noDataWiredStream(t, due, store)
	if err := stream.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	round, err := stream.noDataRoundFor(due, nil, execution.CompletenessFull)
	if err != nil {
		t.Fatal(err)
	}
	if round.mutation == nil {
		t.Fatal("a round that judged the item absent stored nothing")
	}
	if err := stream.coordinator.applyNoDataMemory(context.Background(),
		execution.SlotExecutionRequest{Contract: stream.header.Contract},
		[]execution.PlanNoDataMutation{*round.mutation}); err != nil {
		t.Fatal(err)
	}
	if len(store.applied) != 1 {
		t.Fatalf("stored %d memories, want one", len(store.applied))
	}
	if store.applied[0].Identity.Plan != due.Identity {
		t.Fatalf("stored memory for %+v, want %+v", store.applied[0].Identity.Plan, due.Identity)
	}
}

// A Plan's own completeness decides whether its absence is evidence. Another
// Plan's partial query in the same Slot says nothing about this one, and
// reading the Slot as a whole would silence every no-data Plan whenever any
// query anywhere came back short.
func TestNoDataCompletenessIsPerPlan(t *testing.T) {
	due := noDataWiredPlan(t)
	other := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "8"}
	stream := noDataWiredStream(t, due, &emptyNoDataStore{})
	stream.bindings = []execution.NamedInputBinding{
		{
			Consumer:     execution.ConsumerRef{Plan: due.Identity, LevelID: 5, HasLevel: true},
			Completeness: execution.CompletenessFull,
		},
		{
			Consumer:     execution.ConsumerRef{Plan: other, LevelID: 5, HasLevel: true},
			Completeness: execution.CompletenessPartial,
		},
	}
	if got := stream.noDataCompleteness(due); got != execution.CompletenessFull {
		t.Fatalf("completeness = %q, want %q: another Plan's short query is not this Plan's",
			got, execution.CompletenessFull)
	}

	stream.bindings[0].Completeness = execution.CompletenessPartial
	if got := stream.noDataCompleteness(due); got != execution.CompletenessPartial {
		t.Fatalf("completeness = %q, want %q when this Plan's own query came back short",
			got, execution.CompletenessPartial)
	}
}

// A synthetic series is bound to the no-data level's own EffectiveTime fact.
//
// Two things have to hold and neither is visible without asking. The fact has
// to have been prepared - the preparation walks the declared levels, and the
// no-data level is not one of them - and the binding has to ask for the level
// this series is judged against rather than the declared ones. Either one wrong
// and the evaluation refuses the series for a missing fact, every round,
// forever.
func TestASyntheticSeriesIsBoundToTheNoDataLevelsOwnEffectiveTime(t *testing.T) {
	due := noDataWiredPlan(t)
	header := execution.InternalExecutionHeader{
		Contract: noDataPreflightContract(t, []execution.DuePlan{due}),
		DuePlans: []execution.DuePlan{due},
	}
	prepared, err := prepareAlwaysEffectiveTimeFacts(context.Background(), header)
	if err != nil {
		t.Fatal(err)
	}
	noDataLevel := due.CompiledPlan.NoDataLevel().Definition().LevelID
	consumer := execution.ConsumerRef{Plan: due.Identity, LevelID: noDataLevel, HasLevel: true}
	if _, ok := prepared[consumer]; !ok {
		t.Fatalf("no fact was prepared for the no-data level; prepared %d facts for the declared ones",
			len(prepared))
	}

	items := []execution.StatePreflightItem{{
		Identity: execution.StateKeyIdentity{
			Plan: due.Identity, StateGeneration: due.StateGeneration, SeriesIdentityDigest: "synthetic",
		},
	}}
	bound, err := bindAlwaysEffectiveTimeFacts(header, items, prepared, execution.SeriesKindNoData)
	if err != nil {
		t.Fatal(err)
	}
	if len(bound.EffectiveTimeFacts) != 1 {
		t.Fatalf("bound %d facts, want only the no-data level's", len(bound.EffectiveTimeFacts))
	}
	if got := bound.EffectiveTimeFacts[0].Consumer.LevelID; got != noDataLevel {
		t.Fatalf("bound the fact for level %d, want the no-data level %d", got, noDataLevel)
	}

	// And a real series still binds the declared levels, so the kind is what
	// separates them rather than the no-data level simply winning.
	real, err := bindAlwaysEffectiveTimeFacts(header, items, prepared, execution.SeriesKindReal)
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range real.EffectiveTimeFacts {
		if fact.Consumer.LevelID == noDataLevel {
			t.Fatalf("a real series was bound to the no-data level: %+v", fact)
		}
	}
	if len(real.EffectiveTimeFacts) == 0 {
		t.Fatal("a real series was bound to no levels at all")
	}
}

// The dimensions a Slot saw come back as text, because that is what a group is.
func TestSeenSeriesDimensionsComeBackAsText(t *testing.T) {
	text := dimensionText(map[string]json.RawMessage{
		"bk_target_ip": json.RawMessage(`"10.0.0.1"`),
		"port":         json.RawMessage(`8080`),
	})
	if text["bk_target_ip"] != "10.0.0.1" {
		t.Fatalf("a string dimension came back as %q, want the text inside it", text["bk_target_ip"])
	}
	// A dimension that is not a string keeps its JSON form: rendering it any
	// other way would be inventing a spelling the backend never used.
	if text["port"] != "8080" {
		t.Fatalf("a numeric dimension came back as %q, want its JSON form", text["port"])
	}
}

// A Slot whose synthetic points would fall between the positions its own
// history is kept on is refused, and refused here.
//
// Nothing downstream catches it. The history summary has an alignment check,
// but it compares the window against the point's own time, so an unaligned
// point satisfies it by construction. What the trigger then reports - measured,
// not assumed - is DECIDED_DEGRADED with HISTORY_WARMING, which is the same
// answer a window that is genuinely still filling gives. A Plan permanently off
// the grid would report warming forever, never fire, and look like a strategy
// whose window had not warmed up yet.
func TestAnUnalignedSlotIsRefusedRatherThanReportedAsWarming(t *testing.T) {
	for name, test := range map[string]struct {
		evaluationTime int64
		period         int64
		refused        bool
	}{
		"on the grid":      {evaluationTime: 1788000000, period: 60, refused: false},
		"off by a second":  {evaluationTime: 1788000001, period: 60, refused: true},
		"off by half":      {evaluationTime: 1788000030, period: 60, refused: true},
		"a whole day on":   {evaluationTime: 1788086400, period: 60, refused: false},
		"no period at all": {evaluationTime: 1788000000, period: 0, refused: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := noDataPointGrid(test.evaluationTime, test.period)
			if test.refused && err == nil {
				t.Fatalf("a Slot at %d with a %d-second period was accepted; its points would land "+
					"between the positions its history is kept on, and the round would read as warming",
					test.evaluationTime, test.period)
			}
			if !test.refused && err != nil {
				t.Fatalf("a Slot on the grid was refused: %v", err)
			}
		})
	}
}

// And the refusal reaches the round rather than being swallowed into an
// outcome: an unaligned Slot produces no series and says why.
func TestAnUnalignedSlotStopsTheNoDataRound(t *testing.T) {
	due := noDataWiredPlan(t)
	stream := noDataWiredStream(t, due, &emptyNoDataStore{})
	if err := stream.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The fixture's Slot is on the grid, so move it off by one second - the
	// smallest move that breaks the property, and the one a scheduling change
	// would most plausibly make.
	stream.header.Contract.Slot.EvaluationTime++

	round, err := stream.noDataRoundFor(due, nil, execution.CompletenessFull)
	if err == nil {
		t.Fatalf("an unaligned Slot produced %d series and outcome %q instead of saying so",
			len(round.series), round.outcome)
	}
	if !strings.Contains(err.Error(), "periods") {
		t.Fatalf("error = %v, want it to name the grid it is about", err)
	}
}

// The budget decision itself: what fits, what does not, and what an unset
// budget means.
//
// An unset budget is no bound, not no room. Reading it the other way would turn
// no-data off in every deployment that never set one, and it would do it
// silently - every Plan skipped for budget, on a worker with no budget.
func TestTheNoDataBudgetDecision(t *testing.T) {
	for name, test := range map[string]struct {
		spent, adding, budget uint64
		fits                  bool
	}{
		"room to spare":    {spent: 1, adding: 2, budget: 8, fits: true},
		"exactly fills it": {spent: 6, adding: 2, budget: 8, fits: true},
		"one over":         {spent: 7, adding: 2, budget: 8, fits: false},
		"nothing left":     {spent: 8, adding: 1, budget: 8, fits: false},
		// Zero is no room, the same reading checkEffectCounts uses for this
		// number. It is unreachable - the coordinator refuses a zero budget -
		// and the test below is what makes that the guarantee rather than this.
		"no budget at all": {spent: 0, adding: 1, budget: 0, fits: false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := noDataFitsSlotBudget(test.spent, test.adding, test.budget); got != test.fits {
				t.Fatalf("fits(spent=%d, adding=%d, budget=%d) = %t, want %t",
					test.spent, test.adding, test.budget, got, test.fits)
			}
		})
	}
}

// A Plan whose synthetic series do not fit the Slot's remaining state budget is
// skipped by name, and none of its series are evaluated.
//
// By name because it does not resolve on its own: a history roster only grows,
// so a Plan that did not fit this round will not fit the next one either, and
// folded into any other outcome it reads as a transient. None of its series,
// because a partial set would report the groups that fitted as absent and say
// nothing at all about the rest - a half-answer that looks like a whole one.
func TestAPlanBeyondTheSlotBudgetIsSkippedByNameAndEntirely(t *testing.T) {
	due := noDataWiredPlan(t)
	stream := noDataWiredStream(t, due, &emptyNoDataStore{})
	stream.coordinator.budget.MaxStateMutations = 1
	if err := stream.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Spend the budget first, as an earlier Plan in the same Slot would.
	stream.noDataStateMutations = 1

	// Nothing is evaluated, so nothing reaches the batch - which is itself the
	// assertion: a Plan that was only partly skipped would need the state port
	// this fixture deliberately does not have, and would panic here.
	if err := stream.evaluateNoData(context.Background(), nil, 16); err != nil {
		t.Fatal(err)
	}
	if len(stream.noDataOutcomes) != 1 || stream.noDataOutcomes[0] != nodata.OutcomeSkippedSlotBudget {
		t.Fatalf("outcomes = %+v, want exactly %q", stream.noDataOutcomes, nodata.OutcomeSkippedSlotBudget)
	}
	if stream.noDataStateMutations != 1 {
		t.Fatalf("the skipped Plan spent %d mutations, want it to have spent none",
			stream.noDataStateMutations-1)
	}

	// And the same Plan does produce series when asked directly, so the skip
	// above was the budget rather than the Plan having nothing to say.
	round, err := stream.noDataRoundFor(due, nil, execution.CompletenessFull)
	if err != nil {
		t.Fatal(err)
	}
	if len(round.series) == 0 {
		t.Fatal("the Plan produced no series at all, so the budget test proved nothing")
	}
}

// An evaluated Plan spends the budget its series cost, which is visible in what
// happens to the Plan after it.
//
// Asserted through the effect rather than by reading the counter: a Plan that
// charged nothing would leave room for the next one, and the next one would be
// evaluated instead of skipped. That is the behaviour the charge exists for,
// and it is the one a reader of this code would want to be sure of.
func TestAnEvaluatedPlanSpendsWhatItsSeriesCost(t *testing.T) {
	first := noDataWiredPlan(t)
	second := first
	second.Identity.StrategyID = "8"
	second.CompiledPlan = noDataPreflightPlan(t, "8", &contract.NoDataConfigV1{
		Continuous: 1, Level: 2, AggDimension: []string{"bk_target_ip", "bk_target_cloud_id"},
	})
	duePlans := []execution.DuePlan{first, second}

	stream := &streamedExecution{
		coordinator: &SlotExecutionCoordinator{
			ports: Ports{NoData: &emptyNoDataStore{}, Hosts: SharedHostBusiness, State: failingStatePort{}},
			// One mutation for the whole Slot: the first Plan's one series fits
			// and the second Plan's does not.
			budget: ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxGapMutations: 10,
				MaxStateMutations: 1},
		},
		header: execution.InternalExecutionHeader{
			Contract: noDataPreflightContract(t, duePlans), DuePlans: duePlans,
		},
	}
	if err := stream.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The batch fails on purpose - this fixture has no evaluator - and that is
	// after both Plans have been decided, which is what this reads.
	err := stream.evaluateNoData(context.Background(), nil, 16)
	if err == nil {
		t.Fatal("fixture: the batch was expected to fail, so its success means this read something else")
	}
	if len(stream.noDataOutcomes) != 2 {
		t.Fatalf("outcomes = %+v, want one per Plan", stream.noDataOutcomes)
	}
	if stream.noDataOutcomes[0] != nodata.OutcomeEvaluated {
		t.Fatalf("the first Plan landed on %q, want it evaluated", stream.noDataOutcomes[0])
	}
	if stream.noDataOutcomes[1] != nodata.OutcomeSkippedSlotBudget {
		t.Fatalf("the second Plan landed on %q, want %q - the first Plan's series did not spend the budget",
			stream.noDataOutcomes[1], nodata.OutcomeSkippedSlotBudget)
	}
}

// failingStatePort answers every runtime load with an error, so a test can
// reach the batch without standing up an evaluator.
type failingStatePort struct{}

func (failingStatePort) LoadRuntime(
	context.Context, execution.StatePreflightRequest,
) (execution.StatePreflightResult, error) {
	return execution.StatePreflightResult{}, errors.New("no state in this fixture")
}

func (failingStatePort) AdmitRuntime(
	context.Context, execution.StateApplyRequest,
) (execution.StateAdmissionResult, error) {
	return execution.StateAdmissionResult{}, errors.New("no state in this fixture")
}

func (failingStatePort) ApplyRuntime(
	context.Context, execution.StateApplyRequest,
) (execution.StateApplyResult, error) {
	return execution.StateApplyResult{}, errors.New("no state in this fixture")
}

// One observation per outcome, carrying how many Plans landed there.
//
// Per outcome rather than per Plan, so a Slot with hundreds of no-data Plans
// emits at most four; and carrying the count rather than one, because a Slot's
// worth of Plans folded into a single increment under-reports by however many
// shared the outcome - and under-reporting is the direction nobody checks,
// since the number still moves.
func TestNoDataOutcomesAreReportedOncePerOutcomeWithTheirCount(t *testing.T) {
	recorded := make([]observability.Observation, 0, 4)
	stream := &streamedExecution{
		coordinator: &SlotExecutionCoordinator{
			ports: Ports{Observer: observability.ObserverFunc(
				func(_ context.Context, observation observability.Observation) {
					recorded = append(recorded, observation)
				})},
		},
		noDataOutcomes: []nodata.SlotOutcome{
			nodata.OutcomeEvaluated, nodata.OutcomeSkippedSlotBudget, nodata.OutcomeEvaluated,
			nodata.OutcomeEvaluated,
		},
	}
	stream.observeNoDataOutcomes(context.Background())

	counts := map[string]int{}
	for _, observation := range recorded {
		if observation.Stage != observability.StageNoDataDecided {
			continue
		}
		// The Slot's census shares this stage and is asserted on its own below.
		if observation.NoDataCensus != nil {
			continue
		}
		if observation.NoDataSlot == nil {
			t.Fatalf("an observation at %q carries no facts", observation.Stage)
		}
		if _, repeated := counts[observation.NoDataSlot.Outcome]; repeated {
			t.Fatalf("outcome %q was reported twice in one Slot", observation.NoDataSlot.Outcome)
		}
		counts[observation.NoDataSlot.Outcome] = observation.NoDataSlot.Plans
	}
	if len(counts) != 2 {
		t.Fatalf("reported %+v, want one observation per outcome that occurred", counts)
	}
	if counts[string(nodata.OutcomeEvaluated)] != 3 {
		t.Fatalf("EVALUATED carried %d Plans, want the 3 that landed there",
			counts[string(nodata.OutcomeEvaluated)])
	}
	if counts[string(nodata.OutcomeSkippedSlotBudget)] != 1 {
		t.Fatalf("SKIPPED_SLOT_BUDGET carried %d Plans, want 1",
			counts[string(nodata.OutcomeSkippedSlotBudget)])
	}
	// An outcome nothing landed on is not reported at all - the metric creates
	// its label at startup, so silence here is not a missing series.
	if _, reported := counts[string(nodata.OutcomeSkippedMemoryUnreadable)]; reported {
		t.Fatal("an outcome no Plan landed on was reported")
	}
}

// Every Slot reports how many Plans that detect no-data it found, including
// none.
//
// This is the number that separates "this worker has no such Plan" from "it has
// them and judged none of them". Three releases running, a Plan that never
// reached a decision produced four computed zeros, no log line and no error --
// and a worker with genuinely nothing to do produces exactly the same reading.
// Only a count taken where the Plans are found, before anything is decided,
// tells the two apart.
func TestEverySlotReportsHowManyNoDataPlansItFound(t *testing.T) {
	for name, test := range map[string]struct {
		seen int
	}{
		"a Slot with no such Plan": {seen: 0},
		"a Slot with several":      {seen: 7},
	} {
		t.Run(name, func(t *testing.T) {
			var recorded []observability.Observation
			stream := &streamedExecution{
				coordinator: &SlotExecutionCoordinator{
					ports: Ports{Observer: observability.ObserverFunc(
						func(_ context.Context, observation observability.Observation) {
							recorded = append(recorded, observation)
						})},
				},
				noDataPlansSeen: test.seen,
			}

			stream.observeNoDataOutcomes(context.Background())

			census := 0
			for _, observation := range recorded {
				if observation.NoDataCensus == nil {
					continue
				}
				census++
				if observation.Stage != observability.StageNoDataDecided {
					t.Fatalf("the census was reported at %q", observation.Stage)
				}
				// Labelled as the due hop, which is what puts it in the table
				// beside the published, assembled and frozen counts. Without
				// the label it lands under no hop at all and the last column
				// of that table is empty.
				if observation.NoDataCensus.Hop != observability.NoDataHopDue {
					t.Fatalf("the census was reported under hop %q, want %q",
						observation.NoDataCensus.Hop, observability.NoDataHopDue)
				}
				if observation.NoDataCensus.Plans != test.seen {
					t.Fatalf("census = %d, want the %d Plans the Slot found",
						observation.NoDataCensus.Plans, test.seen)
				}
			}
			if census != 1 {
				t.Fatalf("the Slot reported its census %d times, want exactly once. A Slot that reports "+
					"none would otherwise be indistinguishable from one that never counted", census)
			}
		})
	}
}

// The census counts the Plans the round found, and the outcomes account for
// every one of them.
//
// One pass over one list produces both, which is what makes them comparable in
// production: the leader says how many no-data Plans exist, this says how many
// arrived, and the outcomes say what was decided about them. A census above the
// outcomes is a Plan dropped between being found and being judged.
func TestTheCensusAndTheOutcomesAgreeOnHowManyPlans(t *testing.T) {
	var recorded []observability.Observation
	stream := &streamedExecution{
		coordinator: &SlotExecutionCoordinator{
			ports: Ports{Observer: observability.ObserverFunc(
				func(_ context.Context, observation observability.Observation) {
					recorded = append(recorded, observation)
				})},
		},
		noDataPlansSeen: 4,
		noDataOutcomes: []nodata.SlotOutcome{
			nodata.OutcomeEvaluated, nodata.OutcomeEvaluated,
			nodata.OutcomeSkippedSlotBudget, nodata.OutcomeSkippedQueryNotFull,
		},
	}

	stream.observeNoDataOutcomes(context.Background())

	census, judged := -1, 0
	for _, observation := range recorded {
		switch {
		case observation.NoDataCensus != nil:
			census = observation.NoDataCensus.Plans
		case observation.NoDataSlot != nil:
			judged += observation.NoDataSlot.Plans
		}
	}
	if census != judged {
		t.Fatalf("the Slot found %d Plans and accounted for %d; every Plan the round finds lands on "+
			"exactly one outcome, and a difference here is a Plan that was dropped in between",
			census, judged)
	}
}

// The census is taken from the Slot's own Plans, by the round that walks them.
//
// The tests above set the count on the stream and assert what is reported from
// it; none of them runs the walk that produces it. That gap is the same shape
// as the defect the census exists to catch -- an instrument nobody drives reads
// as a computed zero, and a zero here is the healthy answer on most Slots.
func TestTheCensusCountsTheSlotsOwnNoDataPlans(t *testing.T) {
	withNoData := noDataWiredPlan(t)
	// An ordinary Plan, in the same Slot, that does not detect no-data.
	plain := withNoData
	plain.Identity.StrategyID = "8"
	plain.CompiledPlan = noDataPreflightPlan(t, "8", nil)
	if plain.CompiledPlan.NoData() != nil {
		t.Fatal("the fixture's plain Plan detects no-data, so this test cannot tell the two apart")
	}
	duePlans := []execution.DuePlan{withNoData, plain}

	stream := &streamedExecution{
		coordinator: &SlotExecutionCoordinator{
			ports:  Ports{NoData: &emptyNoDataStore{}, Hosts: SharedHostBusiness, State: failingStatePort{}},
			budget: ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxGapMutations: 10, MaxStateMutations: 1},
		},
		header: execution.InternalExecutionHeader{
			Contract: noDataPreflightContract(t, duePlans), DuePlans: duePlans,
		},
	}
	if err := stream.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The budget is already spent, as an earlier Plan in the same Slot would
	// leave it. The no-data Plan is then skipped by name rather than evaluated,
	// which keeps this test off the state port -- and makes the point sharper:
	// the census counts a Plan the round found, whatever the round then does
	// with it.
	stream.noDataStateMutations = 1

	if err := stream.evaluateNoData(context.Background(), nil, 16); err != nil {
		t.Fatal(err)
	}

	if stream.noDataPlansSeen != 1 {
		t.Fatalf("census = %d over a Slot of one no-data Plan and one ordinary Plan, want 1. It counts "+
			"the Plans that detect no-data, not the Plans the Slot has", stream.noDataPlansSeen)
	}
	// And it accounts for every one of them, which is the relation the page is
	// read by: the leader says how many exist, this says how many arrived, and
	// the outcomes say what was decided.
	if len(stream.noDataOutcomes) != stream.noDataPlansSeen {
		t.Fatalf("the round found %d Plans and recorded %d outcomes; every Plan it finds lands on exactly "+
			"one outcome", stream.noDataPlansSeen, len(stream.noDataOutcomes))
	}
}

// A Slot with no such Plan counts none, taken by the same walk.
func TestTheCensusIsZeroWhenNoPlanDetectsNoData(t *testing.T) {
	plain := noDataWiredPlan(t)
	plain.CompiledPlan = noDataPreflightPlan(t, "7", nil)
	duePlans := []execution.DuePlan{plain}
	stream := &streamedExecution{
		coordinator: &SlotExecutionCoordinator{
			ports:  Ports{NoData: &emptyNoDataStore{}, Hosts: SharedHostBusiness, State: failingStatePort{}},
			budget: ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxGapMutations: 10, MaxStateMutations: 8},
		},
		header: execution.InternalExecutionHeader{
			Contract: noDataPreflightContract(t, duePlans), DuePlans: duePlans,
		},
	}
	if err := stream.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := stream.evaluateNoData(context.Background(), nil, 16); err != nil {
		t.Fatal(err)
	}

	if stream.noDataPlansSeen != 0 {
		t.Fatalf("census = %d on a Slot where no Plan detects no-data, want 0", stream.noDataPlansSeen)
	}
}
