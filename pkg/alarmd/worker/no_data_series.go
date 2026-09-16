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
}

// seriesDimensionsFor is the dimensions of every series this Slot saw for one
// Plan, which is the evidence absence is decided from.
//
// Values come back as text because that is what a group is: the backend keys
// its no-data groups by dimension strings, and a JSON string and the text
// inside it are the same dimension. A dimension that is not a string keeps its
// JSON form, which is the only rendering that does not invent one.
func seriesDimensionsFor(prepared []preparedSeries, plan execution.PlanIdentity) []map[string]string {
	seen := make([]map[string]string, 0, len(prepared))
	for _, entry := range prepared {
		if entry.due.Identity != plan {
			continue
		}
		for _, input := range entry.inputs {
			for _, binding := range input.Inputs {
				if binding.Role != execution.InputRolePrimary || binding.View == nil {
					continue
				}
				record, ok := binding.View.Record(0)
				if !ok {
					continue
				}
				seen = append(seen, dimensionText(record.Dimensions()))
				break
			}
			break
		}
	}
	return seen
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

// noDataRoundFor decides one Plan's absence and builds the synthetic series the
// ordinary evaluation batch judges.
//
// The series are ordinary in every way that matters downstream: a record, a
// dataset, a primary binding, the same batch, the same evaluator, the same
// recovery gate, the same state writer. The one thing that marks them is the
// kind on the input, and that is what decides they are judged against the
// no-data level rather than the strategy's declared ones.
func (stream *streamedExecution) noDataRoundFor(
	due execution.DuePlan, seen []map[string]string, completeness execution.Completeness,
) (noDataRound, error) {
	config := due.CompiledPlan.NoData()
	if config == nil {
		return noDataRound{outcome: nodata.OutcomeNone}, nil
	}
	identity := execution.PlanNoDataIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration}
	snapshot, found := stream.noData.Find(identity)
	if !found {
		return noDataRound{}, fmt.Errorf(
			"alarmd worker: no-data memory for strategy %s was not loaded", due.Identity.StrategyID)
	}
	version, err := execution.BuildApplyVersion(stream.header.Contract, due.StateApplyEpoch)
	if err != nil {
		return noDataRound{}, err
	}
	period := int64(due.CompiledPlan.EvaluationSemantics().EvaluationInterval)
	if err := noDataPointGrid(int64(stream.header.Contract.Slot.EvaluationTime), period); err != nil {
		return noDataRound{}, err
	}
	hosts := stream.noDataHosts[identity]
	decided, err := nodata.EvaluatePlanSlot(nodata.PlanSlotInput{
		NoData:           config,
		Scope:            due.CompiledPlan.TargetScope(),
		Identity:         identity,
		Snapshot:         snapshot,
		ApplyVersion:     version,
		ScheduleRevision: due.ScheduleRevision,
		EvaluationTime:   int64(stream.header.Contract.Slot.EvaluationTime),
		PeriodSeconds:    int64(due.CompiledPlan.EvaluationSemantics().EvaluationInterval),
		Completeness:     completeness,
		Series:           seen,
		KnownHosts:       hosts.Known,
		HostsResolved:    hosts.Resolved,
		OutOfBusiness:    hosts.OutOfBusiness,
	})
	if err != nil {
		return noDataRound{}, err
	}
	round := noDataRound{mutation: decided.Mutation, outcome: decided.Outcome}
	if len(decided.Series) == 0 {
		return round, nil
	}
	view, err := execution.PlanViewFor(due, execution.SeriesKindNoData)
	if err != nil {
		return noDataRound{}, err
	}
	for _, synthetic := range decided.Series {
		entry, err := stream.noDataCompletedSeries(view, synthetic, version)
		if err != nil {
			return noDataRound{}, err
		}
		round.series = append(round.series, entry)
	}
	return round, nil
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
	level := view.CompiledPlan.Levels()[0]
	consumer := execution.ConsumerRef{
		Plan: view.Identity, LevelID: level.Definition().LevelID, HasLevel: true,
	}
	record := contract.CanonicalRecordV2{
		RecordID:          digest,
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
		due: view, series: series,
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
// them a path of their own.
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
	budget := stream.coordinator.slotBudget().MaxStateMutations
	for _, due := range stream.header.DuePlans {
		if due.CompiledPlan.NoData() == nil {
			continue
		}
		// Counted here, in the same pass that decides the outcomes, so the two
		// cannot disagree about which Plans this Slot had. A Plan seen here and
		// missing from the outcomes was dropped between the two.
		stream.noDataPlansSeen++
		round, err := stream.noDataRoundFor(due, seriesDimensionsFor(prepared, due.Identity),
			stream.noDataCompleteness(due))
		if err != nil {
			return err
		}
		// A synthetic series writes state like any other, so it spends from the
		// same per-Slot budget. A Plan whose series do not fit is skipped by
		// name rather than silently trimmed: a history roster only grows, so it
		// will not fit next round either, and a partial set of synthetic series
		// would report the groups that fitted as absent and say nothing about
		// the rest.
		if !noDataFitsSlotBudget(stream.noDataStateMutations, uint64(len(round.series)), budget) {
			stream.noDataOutcomes = append(stream.noDataOutcomes, nodata.OutcomeSkippedSlotBudget)
			continue
		}
		stream.noDataStateMutations += uint64(len(round.series))
		stream.noDataOutcomes = append(stream.noDataOutcomes, round.outcome)
		if round.mutation != nil {
			stream.noDataMutations = append(stream.noDataMutations, *round.mutation)
		}
		for _, entry := range round.series {
			pending = append(pending, entry)
			if len(pending) >= batchLimit {
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}
	return flush()
}

// noDataCompleteness is whether this Slot saw the whole period for one Plan.
//
// Absence is only evidence when the round was complete, and the completeness
// that matters is the Plan's own: a Slot in which another Plan's query came
// back partial says nothing about this one.
func (stream *streamedExecution) noDataCompleteness(due execution.DuePlan) execution.Completeness {
	worst := execution.CompletenessFull
	for _, binding := range planBindings(stream.bindings, due.Identity) {
		if binding.Completeness != execution.CompletenessFull {
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
func (coordinator *SlotExecutionCoordinator) applyNoDataMemory(
	ctx context.Context, request execution.SlotExecutionRequest, mutations []execution.PlanNoDataMutation,
) error {
	if len(mutations) == 0 {
		return nil
	}
	result, err := coordinator.ports.NoData.ApplyNoData(ctx, execution.NoDataApplyRequest{
		Contract: request.Contract, Items: mutations,
	})
	if err != nil {
		return fmt.Errorf("alarmd worker: store no-data memory: %w", err)
	}
	for _, item := range result.Items {
		switch item.Status {
		case execution.NoDataApplied, execution.NoDataAlreadyApplied, execution.NoDataStale, execution.NoDataConflict:
			continue
		case execution.NoDataRetryable:
			continue
		default:
			// A deterministic refusal is this build disagreeing with what it
			// just built, which no retry resolves and which would otherwise be
			// invisible until someone wondered why a duration never grew.
			return fmt.Errorf("alarmd worker: no-data memory for strategy %s was refused: %s",
				item.Identity.Plan.StrategyID, item.ReasonCode)
		}
	}
	return nil
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
		Direction: observability.DirectionInternal, Result: observability.ResultSuccess,
		NoDataCensus: &observability.NoDataCensusFacts{
			Hop: observability.NoDataHopDue, Plans: stream.noDataPlansSeen,
		},
	})
	if len(stream.noDataOutcomes) == 0 {
		return
	}
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
			Operation: observability.Operation(stream.request.Operation),
			Direction: observability.DirectionInternal, Result: observability.ResultSuccess,
			NoDataSlot: &observability.NoDataSlotFacts{Outcome: string(outcome), Plans: plans},
		})
	}
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

// noDataFitsSlotBudget says whether one Plan's synthetic series fit what is
// left of the Slot's state mutation budget.
//
// A zero budget is no room, which is the reading checkEffectCounts already
// uses for the same number. It reads as harsh and it is unreachable: the
// coordinator refuses a zero budget at construction and the configuration
// refuses one before that, so no Slot ever gets here with nothing allowed. An
// earlier version of this read zero as "no bound", which sounds like a safer
// default and is really a second meaning for one number - the kind that agrees
// with the first one until the day it does not.
func noDataFitsSlotBudget(spent, adding, budget uint64) bool {
	return spent+adding <= budget
}
