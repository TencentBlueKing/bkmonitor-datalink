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
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// supplementRun is what a supplement execution carries beside the stream:
// the series it may evaluate, what became of each, and the points of every
// series that reached its evaluation, by Plan, for finalization to settle.
type supplementRun struct {
	series    map[execution.SeriesIdentityDigest]struct{}
	facts     execution.SupplementFacts
	evaluated map[execution.PlanIdentity]map[execution.SeriesIdentityDigest]uint64
}

func newSupplementRun(scope execution.SupplementScope) *supplementRun {
	series := make(map[execution.SeriesIdentityDigest]struct{}, len(scope.Series))
	for _, identity := range scope.Series {
		series[identity] = struct{}{}
	}
	return &supplementRun{series: series,
		evaluated: make(map[execution.PlanIdentity]map[execution.SeriesIdentityDigest]uint64)}
}

// executeSupplement evaluates, for the Slot the request was frozen from, the
// series of its scope that nothing has decided at that Slot yet. See
// execution.SupplementScope for what it writes and what it leaves alone.
//
// It runs the Slot's own query path and evaluation, so a series is decided
// here exactly as the Slot would have decided it had the series been in its
// read, and it differs from the Slot in three places only: it takes the
// scope's series and no other, it skips a series whose State has reached the
// Slot rather than failing on it, and it writes no marker, memory or
// Progress. The outcome of every series is counted by name on the result.
func (coordinator *SlotExecutionCoordinator) executeSupplement(
	ctx context.Context,
	request execution.SlotExecutionRequest,
) (execution.SlotExecutionResult, error) {
	stream := &streamedExecution{coordinator: coordinator, request: request, supplement: newSupplementRun(*request.Supplement)}
	defer stream.releaseProvisional()
	started := time.Now()
	completion, err := coordinator.ports.Query.Execute(ctx, execution.QueryExecutionRequest{
		Contract: request.Contract, Operation: request.Operation, AttemptNo: request.AttemptNo,
	}, stream)
	if err != nil {
		coordinator.observeQueryFailure(ctx, request.Operation, started, "execute", err)
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: supplement query: %w", err)
	}
	if err := stream.complete(ctx, completion); err != nil {
		coordinator.observeQueryFailure(ctx, request.Operation, started, "stream_complete", err)
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: complete supplement: %w", err)
	}
	if len(stream.evaluated.Plans) > 0 {
		err = coordinator.ports.Sequencer.Sequence(ctx, sequencingScope(stream.header, stream.stateItems, stream.gapItems),
			func(sequenceCtx context.Context) error {
				return coordinator.finalizeSupplement(sequenceCtx, request, stream)
			})
		if err != nil {
			return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: finalize supplement: %w", err)
		}
	}
	facts := stream.supplement.facts
	if facts.Decided() != facts.Candidates {
		return execution.SlotExecutionResult{}, fmt.Errorf(
			"alarmd worker: supplement decided %d of its %d series", facts.Decided(), facts.Candidates)
	}
	return execution.SlotExecutionResult{Result: observability.ResultSuccess, ReasonCode: observability.ReasonNone,
		Supplement: &facts, Usage: stream.budgetUsage(), Timing: stream.timing()}, nil
}

// supplementTakes decides, before any State is read, whether a prepared
// series is one this supplement evaluates. A series outside the scope was
// the Slot's own and is not counted; one inside it is counted as a candidate
// and, when it is not taken, by why.
func (stream *streamedExecution) supplementTakes(prepared preparedSeries) bool {
	run := stream.supplement
	if _, named := run.series[prepared.identity]; !named {
		return false
	}
	run.facts.Candidates++
	if len(primaryIncompleteBindings(prepared.inputs)) != 0 {
		run.facts.InputIncomplete++
		return false
	}
	switch stream.noDataStandingAt(prepared) {
	case noDataRecorded:
		run.facts.NoDataFact++
		return false
	case noDataUnknown:
		run.facts.Withheld++
		return false
	}
	return true
}

// supplementReached counts a series whose State has reached the Slot: at the
// Slot itself, which is a supplement of it that already ran, or past it,
// which is a later Slot that decided the series from what it had. Either
// way the Slot is not decided for it a second time.
func (stream *streamedExecution) supplementReached(view execution.RuntimeStateView) bool {
	if view.VersionComparison != execution.ApplyVersionEqual && view.VersionComparison != execution.ApplyVersionPersistedNewer {
		return false
	}
	stream.supplement.facts.CrossedT++
	return true
}

// noteSupplementEvaluated keeps a series that reached its evaluation for
// finalization, and takes the gap statements off its result: a supplement
// reads the markers and never moves them, so nothing it proposes for them
// is carried, counted or written.
func (stream *streamedExecution) noteSupplementEvaluated(
	due execution.DuePlan, series execution.SeriesIdentityDigest,
	inputs []execution.SeriesEvaluationInputRequest, evaluated *execution.EvaluationResult,
) {
	for index := range evaluated.Plans {
		evaluated.Plans[index].GuardBeforeEvents = nil
		evaluated.Plans[index].GuardAfterState = nil
	}
	byPlan := stream.supplement.evaluated[due.Identity]
	if byPlan == nil {
		byPlan = make(map[execution.SeriesIdentityDigest]uint64)
		stream.supplement.evaluated[due.Identity] = byPlan
	}
	byPlan[series] = primaryPoints(inputs)
}

type noDataStanding int

const (
	noDataNone noDataStanding = iota
	// noDataRecorded is a series whose no-data group was recorded absent at
	// the Slot and has not been seen since.
	noDataRecorded
	// noDataUnknown is a series whose Plan's memory could not be read, so
	// whether its group was recorded absent is not known.
	noDataUnknown
)

// noDataStandingAt is what the Plan's no-data memory says about the series'
// group at the Slot.
//
// The memory is read as it is now, and it answers for the Slot while the
// absence it holds began at or before the Slot: an absence stands only until
// the group is seen, a sighting clearing it, so one that began by the Slot
// and still stands covers the Slot, and the group has not been seen since.
// A no-data alert raised on it is left to recover as it would; the
// supplement does not write the Slot over it. A later Slot that saw this
// very series moved its State past the Slot as well, which the State read
// counts on its own.
//
// Two groups answer for a series: its own, and the whole item's. The item
// recorded absent as a whole had nothing at all at the Slot, whatever group
// a late series projects onto, and it is cleared the moment anything
// arrives.
func (stream *streamedExecution) noDataStandingAt(prepared preparedSeries) noDataStanding {
	config := prepared.due.CompiledPlan.NoData()
	if config == nil {
		return noDataNone
	}
	snapshot, found := stream.noData.Find(prepared.due.NoDataIdentity())
	if !found {
		return noDataUnknown
	}
	switch snapshot.Status {
	case execution.NoDataMemoryMissing:
		return noDataNone
	case execution.NoDataMemoryFound:
	default:
		return noDataUnknown
	}
	dimensions, found := primaryDimensions(prepared.inputs)
	if !found {
		return noDataUnknown
	}
	whole := nodata.WholeItemGroup().Key()
	// A series without every agg_dimension name has no group of its own
	// that no-data tracks; the whole item still answers for it.
	own := whole
	if group, tracked := nodata.Project(dimensions, config.AggDimension); tracked {
		own = group.Key()
	}
	at := int64(stream.header.Contract.Slot.EvaluationTime)
	for _, memory := range snapshot.Groups {
		if (memory.GroupKey == own || memory.GroupKey == whole) && memory.FirstAbsent != 0 && memory.FirstAbsent <= at {
			return noDataRecorded
		}
	}
	return noDataNone
}

// guardsAsOfSlot is the gap markers a supplement of the Slot evaluates
// under: the ones loaded, and a marker cleared at the Slot or after it read
// as open. A clearing at the Slot's version or later means the marker stood
// when the Slot was evaluated, and the Slot's NORMAL was withheld under it;
// deciding the Slot again without it would reach a NORMAL the Slot itself
// could not have. A marker opened after the Slot is loaded as it stands and
// withholds too, which is the side a supplement may err on: it may withhold
// a NORMAL the Slot would have reached, and never reach one the Slot would
// have withheld.
func guardsAsOfSlot(header execution.InternalExecutionHeader, loaded execution.GapLoadResult) (execution.GapLoadResult, error) {
	guards := execution.GapLoadResult{Items: append([]execution.GapGuardSnapshot(nil), loaded.Items...)}
	for index, marker := range guards.Items {
		if marker.Status != execution.GapClearedTombstone {
			continue
		}
		due, found := duePlan(header.DuePlans, marker.Identity.Plan)
		if !found {
			continue
		}
		version, err := execution.BuildApplyVersion(header.Contract, due.StateApplyEpoch)
		if err != nil {
			return execution.GapLoadResult{}, err
		}
		if execution.CompareApplyVersion(marker.PersistedApplyVersion, version) == execution.ApplyVersionPersistedOlder {
			continue
		}
		marker.Status = execution.GapFound
		marker.Scopes = []execution.GapScopeState{{Status: execution.GapStatusGapped,
			ReasonCode: execution.ReasonCode(contract.ReasonHistoryGapped), RequiredFullSlots: 1}}
		guards.Items[index] = marker
	}
	return guards, nil
}

// finalizeSupplement writes, Plan by Plan, what the supplement decided: the
// events first and the State after them, as a Slot writes them. What it does
// not write is by name: a Plan whose activation moved since the Slot, or whose
// side effects are no longer admitted, is config_drift; a series whose State
// write would not be the next one is crossed_t; a series that ended with
// nothing to write, or whose output or State was refused, is withheld.
//
// A refusal that is the store or the sink being unavailable returns, as it
// does for a Slot, and nothing after it is written; what was written before
// it stays, and a supplement run again finds those series at the Slot.
func (coordinator *SlotExecutionCoordinator) finalizeSupplement(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	stream *streamedExecution,
) error {
	run := stream.supplement
	activations, err := coordinator.loadActivations(ctx, duePlanActivationRequest(request.Contract, stream.header.DuePlans))
	if err != nil {
		return fmt.Errorf("alarmd worker: supplement activation read: %w", err)
	}
	drifted, _ := changedDuePlanActivations(stream.header.DuePlans, activations)
	// A Plan being made to warm again is one the control plane has restarted;
	// its marker is the Slot path's to write, and until then it is not the
	// Plan the Slot was frozen with.
	for _, fact := range unsatisfiedForcedWarmingActivations(activations, stream.gaps, drifted).Facts {
		drifted[fact.Key()] = struct{}{}
	}
	plans := append([]execution.PlanEvaluationResult(nil), stream.evaluated.Plans...)
	sort.Slice(plans, func(left, right int) bool { return lessPlanIdentity(plans[left].Plan, plans[right].Plan) })
	stateIndex := indexStatePreflight(stream.state)
	for _, planResult := range plans {
		due, ok := duePlan(stream.header.DuePlans, planResult.Plan)
		if !ok {
			return errors.New("alarmd worker: supplemented plan is not due")
		}
		evaluated := run.evaluated[planResult.Plan]
		if _, moved := drifted[due.Key()]; moved {
			run.facts.ConfigDrift += len(evaluated)
			continue
		}
		admitted, err := coordinator.supplementAdmitted(ctx, request, due)
		if err != nil {
			return err
		}
		if !admitted {
			run.facts.ConfigDrift += len(evaluated)
			continue
		}
		if err := coordinator.writeSupplementedPlan(ctx, request, stream, due, planResult, stateIndex, evaluated); err != nil {
			return err
		}
	}
	return nil
}

// writeSupplementedPlan writes one Plan's supplemented series.
func (coordinator *SlotExecutionCoordinator) writeSupplementedPlan(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	stream *streamedExecution,
	due execution.DuePlan,
	planResult execution.PlanEvaluationResult,
	stateIndex map[execution.StateKeyIdentity]int,
	evaluated map[execution.SeriesIdentityDigest]uint64,
) error {
	run := stream.supplement
	stateResults := append([]execution.StateEvaluation(nil), planResult.StateResults...)
	sort.Slice(stateResults, func(left, right int) bool {
		return lessStateIdentity(stateResults[left].Mutation.Identity, stateResults[right].Mutation.Identity)
	})
	mutations := make([]execution.StateMutation, 0, len(stateResults))
	eventsByState := make(map[execution.StateKeyIdentity][]contract.TriggerEventV1, len(stateResults))
	withoutMessageByState := make(map[execution.StateKeyIdentity][]execution.EventWithoutMessage)
	decided := make(map[execution.SeriesIdentityDigest]struct{}, len(stateResults))
	for _, stateResult := range stateResults {
		identity := stateResult.Mutation.Identity
		position, found := stateIndex[identity]
		if !found {
			return errors.New("alarmd worker: supplement evaluation returned state without preflight view")
		}
		decided[identity.SeriesIdentityDigest] = struct{}{}
		classified := execution.ClassifyStateMutationDetail(stream.state.Items[position], stateResult.Mutation)
		switch classified.Disposition {
		case execution.StateProceed:
			mutations = append(mutations, stateResult.Mutation)
			eventsByState[identity] = append([]contract.TriggerEventV1(nil), stateResult.Events...)
			if len(stateResult.WithoutMessage) != 0 {
				withoutMessageByState[identity] = stateResult.WithoutMessage
			}
		case execution.StateAlreadyApplied:
			run.facts.CrossedT++
		default:
			// The view was read in this execution and compared older than the
			// Slot; a mutation that is not the next write against it is a
			// defect of the evaluation, as it is on the Slot path.
			return fmt.Errorf("alarmd worker: supplement state mutation preflight: %s", classified.Disposition)
		}
	}
	for series := range evaluated {
		if _, found := decided[series]; !found {
			run.facts.Withheld++
		}
	}
	if len(mutations) == 0 {
		return nil
	}
	retention, err := execution.DeriveStateRetentionRequirement(due.CompiledPlan)
	if err != nil {
		return fmt.Errorf("alarmd worker: %w", err)
	}
	horizon := coordinator.stateHorizon(due)
	planCtx := observability.ContextWithTraceFields(ctx, observability.TraceFields{
		StrategyID: due.Identity.StrategyID, BusinessID: due.Identity.BusinessID})
	rejected, encodedBytes, held, err := coordinator.admitState(planCtx, request.Operation, request.Contract, retention, horizon, mutations)
	if err != nil {
		return err
	}
	defer held.release()
	accepted := make([]execution.StateMutation, 0, len(mutations)-len(rejected))
	acceptedBytes := make([]admittedState, 0, len(mutations)-len(rejected))
	for index, mutation := range mutations {
		if _, refused := rejected[mutation.Identity]; refused {
			run.facts.Withheld++
			continue
		}
		accepted = append(accepted, mutation)
		acceptedBytes = append(acceptedBytes, encodedBytes[index])
	}
	held.settle(acceptedBytes)
	events, withoutMessage := outputsOf(accepted, eventsByState, withoutMessageByState)
	sortTriggerEvents(events)
	if err := coordinator.writeEvents(ctx, request.Operation, due.Identity, events, withoutMessage); err != nil {
		if notWritten, partial := outputNotWritten(err); partial {
			// The series whose events the sink would not represent keep the
			// State they had, as on the Slot path; the rest go on.
			kept, keptBytes, keepErr := withoutSeriesNotWritten(accepted, acceptedBytes, eventsByState, notWritten)
			if keepErr != nil {
				return keepErr
			}
			run.facts.Withheld += len(accepted) - len(kept)
			accepted, acceptedBytes = kept, keptBytes
			held.settle(acceptedBytes)
		} else {
			_, deferred := outputDeferralReason(err)
			_, refused := outputRejectionReason(err)
			if !deferred && !refused && !isRetryableOutputDependency(err) {
				return err
			}
			// Written or not, the Plan's State stays where it was: State
			// follows the acknowledgement. A supplement is not retried for
			// it; the series are counted and left to the Slots after.
			run.facts.Withheld += len(accepted)
			return nil
		}
	}
	if len(accepted) == 0 {
		return nil
	}
	rejectedApply, err := coordinator.applyState(ctx, request.Operation, request.Contract, request.OwnerFence,
		request.ContentScope, retention, horizon, accepted, acceptedBytes)
	if err != nil {
		return err
	}
	for _, mutation := range accepted {
		if reason, terminal := rejectedApply[mutation.Identity]; terminal {
			return fmt.Errorf("alarmd worker: deterministic State apply violates its successful admission contract: %s", reason)
		}
		run.facts.Admitted++
		run.facts.Points += evaluated[mutation.Identity.SeriesIdentityDigest]
	}
	return nil
}

// supplementAdmitted asks the side-effect admission about one Plan, as a
// Slot asks it before writing: a refusal - the Plan no longer admitted at
// the Slot's epoch, or a fence the store turns away - is an answer, and only
// a failure to ask is an error.
func (coordinator *SlotExecutionCoordinator) supplementAdmitted(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	due execution.DuePlan,
) (bool, error) {
	started := time.Now()
	result, err := coordinator.ports.Admission.Check(ctx, execution.SideEffectAdmissionRequest{
		Contract: request.Contract, Plan: due.Key(), StateApplyEpoch: due.StateApplyEpoch, OwnerFence: request.OwnerFence,
	})
	if err == nil {
		err = result.Validate()
	}
	reason := result.ReasonCode
	named, fenced := ownership.RefusalReason(err)
	if fenced && reason == "" {
		reason = execution.ReasonCode(named)
	}
	coordinator.observe(ctx, observability.ComponentState, observability.StageSideEffectAdmission, request.Operation, started, "", reason, err)
	switch {
	case fenced:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("alarmd worker: supplement side-effect admission: %w", err)
	default:
		return result.Admitted, nil
	}
}

// primaryDimensions is the dimensions of a series' first primary record, as
// no-data groups read them.
func primaryDimensions(inputs []execution.SeriesEvaluationInputRequest) (map[string]string, bool) {
	for _, input := range inputs {
		for _, binding := range input.Inputs {
			if binding.Role != execution.InputRolePrimary || binding.View == nil {
				continue
			}
			if record, ok := binding.View.Record(0); ok {
				return dimensionText(record.Dimensions()), true
			}
		}
	}
	return nil, false
}

// primaryPoints is how many points of the read a series was evaluated on:
// its primary input's records. Every Level reads the same primary rows, so
// the first primary binding answers for all of them.
func primaryPoints(inputs []execution.SeriesEvaluationInputRequest) uint64 {
	for _, input := range inputs {
		for _, binding := range input.Inputs {
			if binding.Role == execution.InputRolePrimary && binding.View != nil {
				return uint64(binding.View.Len())
			}
		}
	}
	return 0
}
