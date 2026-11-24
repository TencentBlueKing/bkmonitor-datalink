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
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// noDataRequirementID names the synthetic series' one input. It is not a
// requirement the query planner knows: no query produces these points, and the
// name exists because a binding has to have one.
const noDataRequirementID = execution.RequirementID("__no_data__")

// noDataRound is one Plan's no-data decision turned into what the Slot does
// with it: series to evaluate, memory to store, and the name of what happened.
type noDataRound struct {
	series   []completedSeries
	mutation *execution.PlanNoDataMutation
	outcome  nodata.SlotOutcome
	// facts is what the round counted about the Plan's groups, and horizon
	// the Plan's frozen tracking horizon it counted them against. Carried out
	// for the observation line; the decision itself is in the outcome.
	facts   nodata.AbsenceFacts
	horizon int64
	// horizonSource is where that horizon came from, as compilation froze it
	// beside the number; empty for none.
	horizonSource string
	// absenceStarts is when each absent series' absence began, by series
	// identity digest: what the output checks a manual close recorded during
	// this round against (decision-024).
	absenceStarts map[string]int64
	// said is how many series the round handed on.
	said int
}

// seriesDimensionsFor is the dimensions of every series this Slot saw for one
// Plan, which is the evidence absence is decided from.
//
// Values come back as text because that is what a group is: the backend keys
// its no-data groups by dimension strings, and a JSON string and the text
// inside it are the same dimension. A dimension that is not a string keeps its
// JSON form, which is the only rendering that does not invent one.
func (stream *streamedExecution) seriesDimensionsFor(prepared []preparedSeries, plan execution.PlanIdentity) []map[string]string {
	seen := make([]map[string]string, 0, len(prepared))
	for _, entry := range prepared {
		if entry.due == nil || entry.due.Identity != plan {
			continue
		}
		if dimensions, found := stream.primaryDimensionsOf(entry); found {
			seen = append(seen, dimensions)
		}
	}
	return seen
}

// primaryDimensionsOf is the dimensions of a prepared series' first record,
// read off the PRIMARY binding of its first input.
func (stream *streamedExecution) primaryDimensionsOf(entry preparedSeries) (map[string]string, bool) {
	var first []execution.NamedInputBinding
	if entry.inputs != nil {
		if len(entry.inputs) == 0 {
			return nil, false
		}
		first = entry.inputs[0].Inputs
	} else if consumers := stream.prepared.consumersByPlan[entry.due.Identity]; len(consumers) != 0 {
		inputs := stream.completionInputs
		for _, requirement := range stream.prepared.requirementsByConsumer[consumers[0]] {
			binding, err := stream.completedBinding(consumers[0], entry.identity, requirement, inputs.physical, inputs.bindings)
			if err == nil {
				first = append(first, binding)
			}
		}
	}
	for _, binding := range first {
		if binding.Role != execution.InputRolePrimary || binding.View == nil {
			continue
		}
		record, ok := binding.View.Record(0)
		if !ok {
			continue
		}
		return dimensionText(record.Dimensions()), true
	}
	return nil, false
}

func dimensionText(raw map[string]json.RawMessage) map[string]string {
	text := make(map[string]string, len(raw))
	for name, value := range raw {
		if unquoted, err := strconv.Unquote(string(value)); err == nil {
			text[name] = unquoted
			continue
		}
		text[name] = string(value)
	}
	return text
}

// noDataCompletedSeries turns one synthetic series into a completed series the
// batch evaluates like any other.
func (stream *streamedExecution) noDataCompletedSeries(
	view execution.DuePlan, synthetic nodata.SyntheticSeries, version execution.ApplyVersion,
) (completedSeries, error) {
	fields := synthetic.IdentityFields()
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	identityFields := make([]contract.DimensionFieldV2, 0, len(names))
	for _, name := range names {
		identityFields = append(identityFields, contract.DimensionFieldV2{Name: name, Value: fields[name]})
	}
	// The derivation a real series' identity comes from, so a synthetic series
	// cannot collide with one by being hashed differently. It cannot collide by
	// carrying the same dimensions either: the no-data tag is one of them, and
	// no real series has it.
	digest, err := contract.DeriveDimensionIdentityDigestV2(
		view.Identity.TenantID, view.Identity.BusinessID, identityFields)
	if err != nil {
		return completedSeries{}, fmt.Errorf("alarmd worker: derive no-data series identity: %w", err)
	}
	// The record id comes from the same derivation a real record's does, for
	// the same reason the identity digest above does.
	//
	// Every record that reaches the platform through the reader carries
	// DeriveRecordIDV2(dimension identity digest, source time), and the reader
	// refuses one that does not. A synthetic record is built here rather than
	// read, so nothing was enforcing it, and this one used to carry the
	// dimension digest itself - a value that is the same for every source time
	// of the series. That makes the invariant "within one state key the record
	// id is a function of the source time" false for exactly these series, and
	// anything that derives an id rather than storing it would refuse every
	// no-data write.
	recordID, err := contract.DeriveRecordIDV2(digest, synthetic.SourceTime)
	if err != nil {
		return completedSeries{}, fmt.Errorf("alarmd worker: derive no-data record id: %w", err)
	}
	level := view.CompiledPlan.Levels().At(0)
	consumer := execution.ConsumerRef{
		Plan: view.Identity, LevelID: level.Definition().LevelID, HasLevel: true,
	}
	record := contract.CanonicalRecordV2{
		RecordID:          recordID,
		SourceTime:        synthetic.SourceTime,
		BusinessID:        view.Identity.BusinessID,
		DimensionIdentity: contract.DimensionIdentityV2{Fields: identityFields, Digest: digest},
		Values: map[string]json.RawMessage{
			strategy.NoDataValueField: json.RawMessage(strconv.Itoa(synthetic.Value)),
			// Carried, not detected: the alert text says how many periods this
			// group has been silent, and the converter has no other way to
			// know. The count itself is decided where the absence is.
			contract.NoDataPeriodFactField: json.RawMessage(strconv.FormatInt(synthetic.Periods, 10)),
		},
		Dimensions:   fields,
		ReceivedTime: synthetic.SourceTime,
	}
	dataset := execution.NewDataset([]contract.CanonicalRecordV2{record})
	datasetView, viewErr := execution.NewDatasetView(dataset, []uint32{0})
	if viewErr != nil {
		return completedSeries{}, fmt.Errorf("alarmd worker: project no-data series %s: %w", digest, viewErr)
	}
	series := execution.SeriesIdentityDigest(digest)
	binding := execution.NamedInputBinding{
		Consumer: consumer, RequirementID: noDataRequirementID, DatasetName: "no_data",
		Role: execution.InputRolePrimary, Dataset: dataset, View: datasetView,
		Completeness: execution.CompletenessFull, DataState: execution.DataStateData,
		Disposition: execution.AccessAvailable, ImpactScope: execution.ImpactPlan,
	}
	return completedSeries{
		due: view, series: series, closing: synthetic.Closing,
		inputs: []execution.SeriesEvaluationInputRequest{{
			Contract: stream.header.Contract, Consumer: consumer, SeriesIdentity: series,
			Kind: execution.SeriesKindNoData, RequirementIDs: []execution.RequirementID{noDataRequirementID},
			Inputs: []execution.NamedInputBinding{binding},
		}},
		item: execution.StatePreflightItem{
			Identity: execution.StateKeyIdentity{
				Plan: view.Identity, StateGeneration: view.StateGeneration, SeriesIdentityDigest: series,
			},
			ApplyVersion: version,
		},
	}, nil
}

// evaluateNoData decides absence for every no-data Plan of this Slot and
// evaluates the series that decision produced.
//
// After the Slot's own series and in the same batches: which groups reported is
// the evidence absence is decided from, so it cannot run earlier, and the
// series it produces are ordinary from here on, so there is no reason to give
// them a path of their own. Each Plan's memory is read in parts and its series
// handed on as they are said (decideNoDataRound), so they are cut into
// windows like any other: the per-Slot caps bound a window, and a Plan whose
// synthetic series outgrow one is written in several rather than skipped.
//
// The series are ordinary in every way that matters downstream: a record, a
// dataset, a primary binding, the same batch, the same evaluator, the same
// recovery gate, the same state writer. The one thing that marks them is the
// kind on the input, and that is what decides they are judged against the
// no-data level rather than the strategy's declared ones. The one exception
// to the gate is the close of a group the roster dropped: it is the absence's
// last word, sent once and then forgotten, so the gate is not asked about it
// (completedSeries.closing).
func (stream *streamedExecution) evaluateNoData(
	ctx context.Context, prepared []preparedSeries, batchLimit int,
) error {
	pending := make([]completedSeries, 0, batchLimit)
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		err := stream.evaluateCompletedSeriesBatch(ctx, pending)
		pending = pending[:0]
		return err
	}
	emit := func(entry completedSeries) error {
		// After every prepared series: a window landed before it lets all of
		// their inputs go.
		entry.position = len(prepared)
		// The synthetic series go through the same batch the real ones do,
		// preflight read included, and they are written like any other. They
		// count in the same census, on both sides: counted on the written side
		// only -- which is where a write is a write -- they put written above
		// read on a live deployment by exactly their number, and the census
		// read as a renewal covering a negative population.
		stream.seriesCensus.Due++
		stream.seriesCensus.Read++
		pending = append(pending, entry)
		if len(pending) >= batchLimit {
			return flush()
		}
		return nil
	}
	for _, due := range stream.header.DuePlans {
		if due.CompiledPlan.NoData() == nil {
			continue
		}
		// Counted here, in the same pass that decides the outcomes, so the two
		// cannot disagree about which Plans this Slot had. A Plan seen here and
		// missing from the outcomes was dropped between the two.
		stream.noDataPlansSeen++
		if refusal, refused := stream.noDataRefused[due.NoDataIdentity()]; refused {
			// Its memory did not fit the share beside what the Slot holds:
			// not judged, not written, its threshold detection unchanged.
			stream.coordinator.observeNoDataMemoryRefusal(ctx, stream.request, refusal)
			stream.recordNoDataOutcome(ctx, due, nodata.OutcomeSkippedSlotBudget)
			continue
		}
		started := time.Now()
		round, err := stream.noDataRoundOf(ctx, due, prepared, emit)
		if err != nil {
			outcome, local := noDataLocalOutcome(err)
			if !local || ctx.Err() != nil {
				return err
			}
			// This Plan's no-data detection did not happen. Its threshold
			// detection did, and the rest of this Slot's Plans have not been
			// looked at yet; both used to be thrown away here, and the Slot
			// retried to compute them again, over a no-data record nobody was
			// asking about. The failure is this Plan's outcome for this Slot.
			//
			// Nothing is remembered either. A round that could not be decided
			// has nothing to write, and a round whose verdicts could not be
			// said must not record that it said them: the next round would
			// count the absence from a checkpoint no alert was ever raised
			// against. An absence window the round said before it failed is
			// out, as an output that went before a failure always is, and the
			// next round, deciding from the memory before this one, says it
			// again.
			stream.observeNoDataLocalFailure(ctx, started, due, outcome, err)
			stream.recordNoDataOutcome(ctx, due, outcome)
			continue
		}
		stream.recordNoDataOutcome(ctx, due, round.outcome)
		stream.noDataJudged = append(stream.noDataJudged, noDataJudgment{due: due, round: round})
		if round.mutation != nil {
			stream.noDataMutations = append(stream.noDataMutations, *round.mutation)
		}
	}
	return flush()
}

// noDataRoundOf is one Plan's round over the present set its series make.
func (stream *streamedExecution) noDataRoundOf(
	ctx context.Context, due execution.DuePlan, prepared []preparedSeries, emit func(completedSeries) error,
) (noDataRound, error) {
	present, err := stream.presentSetFor(ctx, due, prepared)
	if err != nil {
		return noDataRound{}, err
	}
	return stream.decideNoDataRound(ctx, due, present, stream.noDataCompleteness(due), emit)
}

// noteAnswerTruncation marks the binding's Plan when the binding is a
// primary input and its physical query's answer may have been cut. Only a
// primary answer says which groups reported; a cut dependency answer leaves
// the Plan's presence as it was.
func (stream *streamedExecution) noteAnswerTruncation(binding execution.NamedInputBinding, route execution.ProviderRouteFacts) {
	if binding.Role != execution.InputRolePrimary || route.Truncation == nil {
		return
	}
	if stream.answerTruncated == nil {
		stream.answerTruncated = map[execution.PlanIdentity]bool{}
	}
	stream.answerTruncated[binding.Consumer.Plan] = true
}

// noDataCompleteness is whether this Slot saw the whole period for one Plan.
//
// Absence is only evidence when the round was complete, and the completeness
// that matters is the Plan's own: a Slot in which another Plan's query came
// back partial says nothing about this one.
func (stream *streamedExecution) noDataCompleteness(due execution.DuePlan) execution.Completeness {
	// Unavailable outranks partial: whichever order the bindings come in, the
	// Plan's round is reported as short as its shortest binding.
	worst := execution.CompletenessFull
	for _, binding := range planBindings(stream.bindings, due.Identity) {
		switch binding.Completeness {
		case execution.CompletenessFull:
		case execution.CompletenessPartial:
			if worst == execution.CompletenessFull {
				worst = binding.Completeness
			}
		default:
			worst = binding.Completeness
		}
	}
	return worst
}

// applyNoDataMemory stores what each no-data Plan now remembers.
//
// A refused write is not a failed Slot. The memory is what the next round reads
// to say how long a group has been absent and which groups it expects; a round
// that could not store it reports from the memory it had, which is the previous
// round's, and the round after that stores again. Failing the Slot over it
// would throw away evaluations that were already correct and already sent.
//
// A memory for a Plan this round does not have, or one whose retention cannot
// be derived, is not a refusal: nothing was asked of the store, the wiring
// handed it a write it cannot place, and that does fail the Slot.
// withoutWithheldNoData is the memory mutations of the Plans whose output
// landed, in the order they were queued.
func withoutWithheldNoData(
	mutations []execution.PlanNoDataMutation, withheld map[execution.PlanIdentity]struct{},
) []execution.PlanNoDataMutation {
	if len(withheld) == 0 {
		return mutations
	}
	kept := make([]execution.PlanNoDataMutation, 0, len(mutations))
	for _, mutation := range mutations {
		if _, skip := withheld[mutation.Identity.Plan]; skip {
			continue
		}
		kept = append(kept, mutation)
	}
	return kept
}

func (coordinator *SlotExecutionCoordinator) applyNoDataMemory(
	ctx context.Context, request execution.SlotExecutionRequest, duePlans []execution.DuePlan,
	mutations []execution.PlanNoDataMutation,
) error {
	if len(mutations) == 0 {
		return nil
	}
	defer retainedOwnerFrom(ctx).enter(slotPhaseNoDataWrite)()
	held, mutations, refused, err := coordinator.holdNoDataFirstWrites(ctx, request, mutations)
	if err != nil {
		return err
	}
	defer coordinator.letGoRetained(retainedOwnerFrom(ctx), held)
	for _, item := range refused {
		coordinator.observeNoDataMemoryRefusal(ctx, request, item)
	}
	if len(mutations) == 0 {
		return nil
	}
	// What each memory lives for: its own Plan's retention, the one the load
	// renews it to.
	written := make([]execution.DuePlan, 0, len(mutations))
	for _, mutation := range mutations {
		due, ok := duePlan(duePlans, mutation.Identity.Plan)
		if !ok {
			return fmt.Errorf("alarmd worker: no-data memory for a Plan that is not due")
		}
		written = append(written, due)
	}
	retention, err := generationRetentionOf(written...)
	if err != nil {
		return err
	}
	if err := execution.EnterCommit(ctx, execution.SlotStageState); err != nil {
		return err
	}
	result, err := coordinator.ports.NoData.ApplyNoData(ctx, execution.NoDataApplyRequest{
		Contract: request.Contract, Items: mutations, Retention: retention,
		Attempt: execution.NoDataWriteAttempt{OwnerEpoch: request.OwnerFence.OwnerEpoch, AttemptNo: request.AttemptNo},
	})
	if err != nil {
		return fmt.Errorf("alarmd worker: store no-data memory: %w", err)
	}
	for _, item := range result.Items {
		switch item.Status {
		case execution.NoDataApplied, execution.NoDataAlreadyApplied, execution.NoDataStale,
			execution.NoDataConflict, execution.NoDataRetryable:
			// Reported, including the ones that worked. Whether a Plan's
			// memory is being kept is a question about the present, and a
			// reader with only the failures has to answer it from an absence
			// of them -- which reads the same whether the Plan recovered, or
			// stopped being evaluated, or started losing races instead.
			coordinator.observeNoDataMemoryWrite(ctx, request, item)
		default:
			// Reported, not raised. A deterministic refusal is one no retry
			// resolves, so failing the Slot over it does not store the record
			// and does throw away the threshold results this round already
			// computed and sent -- every round, for as long as the condition
			// lasts. That is this Plan's ordinary detection stopped, and the
			// other Plans of the Query Group with it, over a record nobody was
			// looking at.
			//
			// What failing the Slot did give was visibility, and that is what
			// this line is for. It names the Plan, the store's own reason and,
			// for a refusal about size, the two numbers it compared -- which
			// is more than the failed Slot carried: that one reached the page
			// as internal_unknown on a retry.
			coordinator.observeNoDataMemoryRefusal(ctx, request, item)
		}
	}
	return nil
}

// observeNoDataMemoryWrite reports what became of one Plan's memory write.
//
// One line per Plan per Slot, on every outcome including the ordinary one. The
// volume is the volume of no-data Plans, which is the same order as the Slot
// lines beside it, and the repeated-line budget bounds it by (reason, Query
// Group) like every other workflow stage.
func (coordinator *SlotExecutionCoordinator) observeNoDataMemoryWrite(
	ctx context.Context, request execution.SlotExecutionRequest, item execution.NoDataApplyItemResult,
) {
	stored := execution.NoDataWriteStored(item.Status)
	result := observability.Result(observability.ResultDegraded)
	if stored {
		result = observability.Result(observability.ResultSuccess)
	}
	coordinator.emitObservation(ctx, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageNoDataMemoryWritten,
		Operation:  observability.Operation(request.Operation),
		Result:     result,
		ReasonCode: observability.ReasonCode(item.ReasonCode),
		Trace: observability.TraceFields{
			StrategyID: item.Identity.Plan.StrategyID, BusinessID: item.Identity.Plan.BusinessID,
		},
		NoDataMemoryWrite: noDataMemoryWriteFacts(item, stored),
	})
}

// noDataMemoryWriteFacts is what the line says about one write.
//
// The conflict, when there is one, travels with it. A conflict carries no
// reason code -- it is a comparison the write lost, not a rejection -- so
// without these values the line reports that the write did not happen and
// nothing about which comparison refused it or what the two sides were. That
// is not a shortcoming anybody would notice until it mattered: a fleet whose
// every memory write was refused read as reason_not_reported for a day, while
// the store had the failing comparison in hand the whole time.
func noDataMemoryWriteFacts(
	item execution.NoDataApplyItemResult, stored bool,
) *observability.NoDataMemoryWriteFacts {
	facts := &observability.NoDataMemoryWriteFacts{Outcome: string(item.Status), Stored: stored}
	if conflict := item.Conflict; conflict != nil {
		facts.DerivedFrom = string(conflict.DerivedFrom)
		facts.Conflict = &observability.NoDataMemoryConflictFacts{
			Kind:             string(conflict.Kind),
			Persisted:        string(conflict.Persisted),
			Proposed:         string(conflict.Proposed),
			ExpectedRevision: conflict.ExpectedRevision,
			StoredRevision:   conflict.StoredRevision,
		}
	}
	return facts
}

// observeNoDataMemoryRefusal reports one Plan the store would not take a
// memory for.
func (coordinator *SlotExecutionCoordinator) observeNoDataMemoryRefusal(
	ctx context.Context, request execution.SlotExecutionRequest, item execution.NoDataApplyItemResult,
) {
	facts := observability.NoDataMemoryRefusalFacts{Reason: string(item.ReasonCode)}
	if size := item.Size; size != nil {
		facts.Record, facts.Groups, facts.Limit, facts.LimitSource = string(size.Record), size.Groups, size.Limit, size.LimitSource
		facts.Bytes = size.Bytes
	}
	coordinator.emitObservation(ctx, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageNoDataMemoryRefused,
		Operation:  observability.Operation(request.Operation),
		Result:     observability.ResultDegraded,
		ReasonCode: observability.ReasonCode(item.ReasonCode),
		Trace: observability.TraceFields{
			StrategyID: item.Identity.Plan.StrategyID, BusinessID: item.Identity.Plan.BusinessID,
		},
		NoDataMemoryRefusal: &facts,
	})
}

// observeNoDataOutcomes reports what happened to every no-data Plan this Slot,
// one observation per outcome that occurred.
//
// Every Plan that detects no-data lands on exactly one outcome, so the four
// counts partition them - and that is the reading the page needs, because the
// three that did not judge look identical once the round is over. The metric
// creates all four labels at startup, so a zero on the one that does not
// resolve on its own can be told from a label nothing ever wrote.
func (stream *streamedExecution) observeNoDataOutcomes(ctx context.Context) {
	// The census first, and before any early return. Every outcome below is
	// conditional on a Plan reaching a decision, so a Slot whose Plans never
	// got there said nothing at all - no observation, no line, no error - and
	// read exactly like a worker with no such Plan to begin with. This is the
	// number that separates the two, so it is the one thing reported
	// unconditionally.
	stream.coordinator.emitObservation(ctx, observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageNoDataDecided,
		Operation: observability.Operation(stream.request.Operation),
		Result:    observability.ResultSuccess,
		NoDataCensus: &observability.NoDataCensusFacts{
			Hop: observability.NoDataHopDue, Plans: stream.noDataPlansSeen,
		},
	})
	if len(stream.noDataOutcomes) == 0 {
		return
	}
	stream.noteNoDataStreaks(ctx)
	counts := make(map[nodata.SlotOutcome]int, len(nodata.SlotOutcomes))
	for _, outcome := range stream.noDataOutcomes {
		counts[outcome]++
	}
	for _, outcome := range nodata.SlotOutcomes {
		plans := counts[outcome]
		if plans == 0 {
			continue
		}
		stream.coordinator.emitObservation(ctx, observability.Observation{
			Component: observability.ComponentEvaluation, Stage: observability.StageNoDataDecided,
			Operation:  observability.Operation(stream.request.Operation),
			Result:     observability.ResultSuccess,
			NoDataSlot: &observability.NoDataSlotFacts{Outcome: string(outcome), Plans: plans},
		})
	}
	// Each judging Plan's own line, for the judgements the Slot kept.
	for _, judged := range stream.noDataJudged {
		if index, filed := stream.noDataOutcomeIndex[judged.due.Key()]; filed && stream.noDataOutcomes[index] == nodata.OutcomeEvaluated {
			stream.observeNoDataAbsence(ctx, judged.due, judged.round)
		}
	}
}

// noDataJudgment is one Plan's round that judged, held for its line.
type noDataJudgment struct {
	due   execution.DuePlan
	round noDataRound
}

// closeNoDataBeyondSlotBudget settles the no-data side of a Slot whose own
// output went past a per-Slot cap: nothing the Slot evaluated reaches State
// or Events, so no Plan's no-data was decided whatever its round had filed,
// and no memory is written for it -- a round whose verdicts were not said
// must not record that it said them. Every no-data Plan lands on exactly one
// outcome: SKIPPED_SLOT_BUDGET where it had none, or where it had filed
// EVALUATED, which counts as a skip for its streak; a skip it had already
// filed for another reason keeps that reason. The census is the Plans the
// Slot had, counted here because the trip can come before they were seen.
//
// landed is, for a Slot some windows of which were written, the Plans every
// window of which lands (completeWindowsBeyondSlotBudget): their rounds were
// said, whole, and keep their outcome and their memory. Nil keeps none.
func (stream *streamedExecution) closeNoDataBeyondSlotBudget(ctx context.Context, landed map[execution.PlanIdentity]struct{}) {
	var kept []execution.PlanNoDataMutation
	for _, mutation := range stream.noDataMutations {
		if _, whole := landed[mutation.Identity.Plan]; whole {
			kept = append(kept, mutation)
		}
	}
	stream.noDataMutations = kept
	seen := 0
	for _, due := range stream.header.DuePlans {
		if due.CompiledPlan.NoData() == nil {
			continue
		}
		seen++
		index, filed := stream.noDataOutcomeIndex[due.Key()]
		_, whole := landed[due.Identity]
		switch {
		case !filed:
			stream.recordNoDataOutcome(ctx, due, nodata.OutcomeSkippedSlotBudget)
		case whole:
		case stream.noDataOutcomes[index] == nodata.OutcomeEvaluated:
			stream.noDataOutcomes[index] = nodata.OutcomeSkippedSlotBudget
		}
	}
	stream.noDataPlansSeen = seen
}

// observeNoDataAbsence reports what one Plan's round decided about its groups,
// on the round it decided.
//
// Only a round that judged has anything to say: the skipped outcomes have
// their partition line and counted nothing, and a line of zeros under them
// would read as a Plan with no groups. One line per judging Plan per Slot,
// the same volume as the memory write beside it, bounded the same way.
//
// The counts are the evaluation's own, counted where each group was decided.
// This is what turns a horizon that has been switched on into a number: how
// many absences it stopped this round, how many it is holding down, and
// against which horizon -- which before this line lived only inside the
// persisted memory and the alerts that stopped arriving.
func (stream *streamedExecution) observeNoDataAbsence(
	ctx context.Context, due execution.DuePlan, round noDataRound,
) {
	if round.outcome != nodata.OutcomeEvaluated {
		return
	}
	facts := round.facts
	stream.coordinator.emitObservation(ctx, observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageNoDataDecided,
		Operation: observability.Operation(stream.request.Operation),
		Result:    observability.ResultSuccess,
		Trace: observability.TraceFields{
			StrategyID: due.Identity.StrategyID, BusinessID: due.Identity.BusinessID,
		},
		NoDataAbsence: &observability.NoDataAbsenceFacts{
			Outcome: string(round.outcome), HorizonSeconds: round.horizon, HorizonSource: round.horizonSource,
			RosterSource: string(facts.RosterSource),
			Expected:     facts.Expected, Present: facts.Present, Absent: facts.Absent, Unavailable: facts.Unavailable,
			Dropped: facts.Dropped, Expired: facts.Expired, Suppressed: facts.Suppressed, ManualClosed: facts.ManualClosed,
			AbsentAges: observability.NoDataAbsentAges{
				ThisRound: facts.AbsentAges.ThisRound, UnderHour: facts.AbsentAges.UnderHour,
				UnderDay: facts.AbsentAges.UnderDay, DayOrMore: facts.AbsentAges.DayOrMore,
			},
		},
	})
}

// noDataPointGrid refuses a Slot whose synthetic points would not land on the
// grid the stored history is kept on.
//
// The points of one series have to fall on one set of positions, because the
// window is read by position: a point one period behind an unaligned Slot lands
// between the positions the last rounds wrote, and the window finds nothing.
//
// It is a refusal because the alternative is silence. Measured on the real
// trigger, an unaligned round comes back DECIDED_DEGRADED with HISTORY_WARMING
// - the same answer a window that is genuinely still filling gives - so a Plan
// whose Slots are permanently off the grid would report warming forever and
// never fire, and nothing anywhere would say why. Nothing validates it further
// down: the history summary's own alignment check compares the window against
// the point's own time, so it is satisfied by construction and never sees this.
func noDataPointGrid(evaluationTime, period int64) error {
	if period <= 0 {
		return fmt.Errorf("alarmd worker: no-data needs a positive period, got %d", period)
	}
	if evaluationTime%period != 0 {
		return fmt.Errorf(
			"alarmd worker: Slot at %d is not a whole number of %d-second periods, so its no-data points "+
				"would fall between the positions its own history is kept on", evaluationTime, period)
	}
	return nil
}
