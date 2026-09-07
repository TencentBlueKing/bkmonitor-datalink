package evaluation

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

type Limits struct {
	MaxPlans, MaxRecords, MaxLevels uint64
	Trigger                         trigger.EvaluationLimitsV2
}

type Evaluator struct {
	detect *detect.Evaluator
	limits Limits
}

func New(detector *detect.Evaluator, limits Limits) (*Evaluator, error) {
	if detector == nil || limits.MaxPlans == 0 || limits.MaxRecords == 0 || limits.MaxLevels == 0 {
		return nil, errors.New("alarmd evaluation: evaluator and positive limits are required")
	}
	return &Evaluator{detect: detector, limits: limits}, nil
}

func (e *Evaluator) Evaluate(ctx context.Context, request execution.EvaluationRequest) (execution.EvaluationResult, error) {
	if e == nil || len(request.Inputs) == 0 || e.limits.MaxPlans < 1 {
		return execution.EvaluationResult{}, errors.New("alarmd evaluation: invalid or over-budget request")
	}
	plan, err := e.evaluateSeries(ctx, request.Header, request.Inputs, request.State, request.Gaps)
	if err != nil {
		return execution.EvaluationResult{}, err
	}
	result := execution.EvaluationResult{Contract: request.Header.Contract, Plans: []execution.PlanEvaluationResult{plan},
		Result: observability.ResultSuccess, ReasonCode: observability.ReasonNone}
	switch plan.Disposition {
	case execution.PlanDecided:
	case execution.PlanDecidedDegraded, execution.PlanUnavailable, execution.PlanReadinessGap:
		result.Result, result.ReasonCode = observability.ResultDegraded, plan.ReasonCode
	case execution.PlanTerminal:
		result.Result, result.ReasonCode = observability.ResultTerminal, plan.ReasonCode
	case execution.PlanRetryPending:
		result.Result, result.ReasonCode = observability.ResultRetrying, plan.ReasonCode
	default:
		return execution.EvaluationResult{}, errors.New("alarmd evaluation: invalid named-input Plan result")
	}
	return result, nil
}

func planGapRecoveryMutation(
	request execution.EvaluationRequest,
	due execution.DuePlan,
	hasStateMutation bool,
) (*execution.PlanGapMutation, error) {
	if !hasStateMutation {
		return nil, nil
	}
	identity := execution.PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration}
	gap, found := request.Gaps.Find(identity)
	if !found || gap.Status != execution.GapFound || gap.LastScheduleRevision != due.ScheduleRevision {
		return nil, nil
	}
	scopes := make([]execution.GapScopeMutation, len(gap.Scopes))
	for index, current := range gap.Scopes {
		scopes[index] = execution.GapScopeMutation{Scope: current.Scope, Kind: execution.GapClear}
		if current.ObservedFullSlots+1 < current.RequiredFullSlots {
			scopes[index].Kind = execution.GapWarmup
			scopes[index].ReasonCode = current.ReasonCode
			scopes[index].RequiredFullSlots = current.RequiredFullSlots
		}
	}
	version, err := execution.BuildApplyVersion(request.Header.Contract, due.StateApplyEpoch)
	if err != nil {
		return nil, err
	}
	mutation, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{
		Identity:               identity,
		ExpectedMarkerRevision: gap.MarkerRevision,
		ApplyVersion:           version,
		ScheduleRevision:       due.ScheduleRevision,
		Scopes:                 scopes,
	})
	if err != nil {
		return nil, err
	}
	return &mutation, nil
}

type recordResult struct {
	outcomes []execution.LevelOutcome
	state    *execution.StateEvaluation
}

type recordDetector func() ([]detect.LevelFact, []detect.ProjectedValue, error)

func (e *Evaluator) evaluateRecordWith(ctx context.Context, request execution.EvaluationRequest, due execution.DuePlan, record execution.RecordView, view execution.RuntimeStateView, guardConvergence map[uint32]bool, run recordDetector) (recordResult, error) {
	series := execution.SeriesIdentityDigest(record.DimensionIdentity().Digest)
	identity := execution.StateKeyIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration, SeriesIdentityDigest: series}
	if view.Identity != identity {
		return recordResult{}, errors.New("alarmd evaluation: runtime state identity mismatch")
	}
	if view.Status == execution.StateRetryableIO || view.Status == execution.StateDeterministicInvalid {
		return e.constrainedRecord(request, due, record, view)
	}
	levels := due.CompiledPlan.Levels()
	reqs := make([]state.LevelRequirement, len(levels))
	for i, l := range levels {
		reqs[i] = state.LevelRequirement{LevelID: l.Definition().LevelID, DetectFingerprint: l.Fingerprints().Detect, RequiredPoints: l.RequiredDetectHistoryPoints(), RetentionPoints: l.StateRequirement().RetentionPoints, EvaluationInterval: time.Duration(l.Trigger().StepSeconds) * time.Second}
	}
	window, err := state.NewWindow(reqs)
	if err != nil {
		return recordResult{}, err
	}
	if len(view.History) > 0 {
		if _, err = window.Apply(toStatePoints(view.History)); err != nil {
			return recordResult{}, err
		}
	}
	facts, projected, err := run()
	if err != nil {
		return recordResult{}, err
	}
	point := state.StatePoint{RecordID: record.RecordID(), SourceTime: record.SourceTime(), Levels: make([]state.PointLevelFact, len(facts))}
	for i, f := range facts {
		point.Levels[i] = state.PointLevelFact{LevelID: f.Definition.LevelID, DetectFingerprint: f.DetectFingerprint, Result: stateFact(f.Result)}
	}
	if _, err = window.Apply([]state.StatePoint{point}); err != nil {
		return recordResult{}, err
	}
	histories := make([]trigger.LevelHistory, len(levels))
	historyCompleteness := make(map[uint32]execution.HistoryCompleteness, len(levels))
	// durableGuardReasons holds, per Level, the reason of the guard that is
	// active in durable state before this record: the loaded Level's WARMING or
	// GAPPED reason, superseded by a loaded Plan or Level gap marker. It is the
	// same set the result contract reads as the active guard, so it also keeps
	// a WARMING Level whose loaded history already allows convergence: a record
	// that does not converge it leaves that guard in place, and every UNKNOWN
	// outcome under it must preserve its reason.
	durableGuardReasons := make(map[uint32]execution.ReasonCode, len(levels))
	effective := make([]trigger.LevelEffectiveTimeFact, len(levels))
	for i, l := range levels {
		h, _ := window.History(l.Definition().LevelID)
		completeness := ""
		if current, found := levelState(view, l.Definition().LevelID); found {
			guarded := current.HistoryCompleteness == execution.HistoryGapped || current.HistoryCompleteness == execution.HistoryWarming
			if guarded && current.GapReasonCode != "" {
				durableGuardReasons[l.Definition().LevelID] = current.GapReasonCode
			}
			// A WARMING or GAPPED Level keeps forcing its completeness onto the
			// trigger until the loaded history already forms the required full
			// window at the last processed record; from then on the live window
			// decides, so the guard converges on the first FULL record. Before
			// this also covered GAPPED, a Plan whose gap episode was cleared
			// after RequiredFullSlots == 1 data Slot stayed GAPPED for ever.
			if guarded && !guardConvergence[l.Definition().LevelID] {
				completeness = string(current.HistoryCompleteness)
			}
		}
		if guarded, reason, found := gapCompleteness(request.Gaps, due, l.Definition().LevelID); found {
			completeness = guarded
			durableGuardReasons[l.Definition().LevelID] = reason
		}
		histories[i] = trigger.LevelHistory{LevelID: l.Definition().LevelID, View: historyView{HistoryView: h, completeness: completeness}}
		summary := histories[i].View.Summarize(record.SourceTime(), l.RequiredDetectHistoryPoints())
		historyCompleteness[l.Definition().LevelID] = execution.HistoryCompleteness(summary.Completeness)
		fact, found := effectiveFact(request.Header, due.Identity, l.Definition().LevelID, series)
		if !found {
			return recordResult{}, errors.New("alarmd evaluation: EffectiveTime fact missing")
		}
		effective[i] = trigger.LevelEffectiveTimeFact{LevelID: l.Definition().LevelID, Fact: fact}
	}
	tfacts := make([]trigger.DetectionFact, len(facts))
	for i, f := range facts {
		var projection, algorithm, group *uint32
		if f.Evidence.HasProjectedValue {
			value := f.Evidence.ProjectedValueOrdinal
			projection = &value
		}
		if f.Evidence.HasMatchedAlgorithm {
			value := f.Evidence.MatchedAlgorithmOrdinal
			algorithm = &value
		}
		if f.Evidence.HasMatchedGroup {
			value := f.Evidence.MatchedGroupOrdinal
			group = &value
		}
		tfacts[i] = trigger.DetectionFact{Definition: f.Definition, DetectFingerprint: f.DetectFingerprint, Result: f.Result, ReasonCode: f.ReasonCode, Evidence: trigger.DetectionEvidence{PredicateDigest: f.Evidence.PredicateDigest, ProjectedValueOrdinal: projection, MatchedAlgorithmOrdinal: algorithm, MatchedGroupOrdinal: group, ResultReason: f.Evidence.ResultReason}}
	}
	tvalues := make([]trigger.ProjectedValue, len(projected))
	for i, v := range projected {
		tvalues[i] = trigger.ProjectedValue{CanonicalDecimal: v.CanonicalDecimal, Available: v.Available, ReasonCode: v.ReasonCode}
	}
	tr, err := trigger.EvaluateV2(trigger.EvaluationRequestV2{TenantID: due.Identity.TenantID, BusinessID: due.Identity.BusinessID, Plan: due.CompiledPlan, Record: trigger.DetectionRecord{RecordID: record.RecordID(), SourceTime: record.SourceTime(), ProjectedValues: tvalues, LevelFacts: tfacts}, RecordRef: contract.TriggerRecordRefV1{RecordID: record.RecordID(), SourceTime: record.SourceTime(), DimensionIdentityDigest: string(series), Dimensions: record.Dimensions()}, Observed: contract.TriggerObservedV1{Values: record.Values()}, Histories: histories, EffectiveTimeFacts: effective, EvaluationTime: int64(request.Header.Contract.Slot.EvaluationTime), ExecutionID: request.Header.ExecutionID, Limits: e.limits.Trigger})
	if err != nil {
		return recordResult{}, err
	}
	outcomes := make([]execution.LevelOutcome, len(tr.LevelOutcomes))
	for i, o := range tr.LevelOutcomes {
		kind := execution.LevelOutcomeKind(o.Result)
		reason := execution.ReasonCode(observability.ReasonNone)
		if kind == "" {
			kind = execution.LevelOutcomeUnknown
			reason = execution.ReasonCode(o.UnavailableReason)
			if reason == "" {
				reason = execution.ReasonCode(o.SuppressedReason)
			}
			// The result contract requires an UNKNOWN outcome under an active
			// guard to carry that guard's reason, whether the trigger, a missing
			// dependency point or the EffectiveTime made it UNKNOWN. Only a
			// record that converges the guard keeps its own local reason.
			if guarded, found := durableGuardReasons[o.LevelID]; found && guardStaysActive(o, historyCompleteness[o.LevelID]) {
				reason = guarded
			}
		}
		outcomes[i] = execution.LevelOutcome{Plan: due.Identity, LevelID: o.LevelID, SeriesIdentityDigest: series, Record: execution.RecordAnchor{RecordID: record.RecordID(), SourceTime: record.SourceTime()}, Outcome: kind, ReasonCode: reason}
	}
	advance := false
	for _, outcome := range tr.LevelOutcomes {
		if outcome.StateDisposition == trigger.StateAdvance {
			advance = true
		}
	}
	var events []contract.TriggerEventV1
	if tr.TriggerEvent != nil {
		events = []contract.TriggerEventV1{*tr.TriggerEvent}
	}
	result := recordResult{outcomes: outcomes}
	if advance {
		mutation, err := buildMutation(request, due, record, view, facts, tr.LevelOutcomes, historyCompleteness, durableGuardReasons)
		if err != nil {
			return recordResult{}, err
		}
		result.state = &execution.StateEvaluation{Mutation: mutation, Events: events}
	}
	return result, nil
}

// evaluateSeries evaluates one due Plan and one series. The slice contains one
// named-input request per compiled Level and must exactly cover the Plan.
func (e *Evaluator) evaluateSeries(
	ctx context.Context,
	header execution.InternalExecutionHeader,
	inputs []execution.SeriesEvaluationInputRequest,
	stateResult execution.StatePreflightResult,
	gaps execution.GapLoadResult,
) (execution.PlanEvaluationResult, error) {
	if e == nil || len(inputs) == 0 {
		return execution.PlanEvaluationResult{}, errors.New("alarmd evaluation: named inputs are required")
	}
	first := inputs[0]
	if first.Contract != header.Contract || first.Consumer.Plan.Validate() != nil || first.SeriesIdentity == "" {
		return execution.PlanEvaluationResult{}, errors.New("alarmd evaluation: named input identity differs from frozen header")
	}
	var due execution.DuePlan
	foundPlan := false
	for _, candidate := range header.DuePlans {
		if candidate.Identity == first.Consumer.Plan {
			due, foundPlan = candidate, true
			break
		}
	}
	if !foundPlan || due.CompiledPlan == nil || uint64(len(due.CompiledPlan.Levels())) > e.limits.MaxLevels {
		return execution.PlanEvaluationResult{}, errors.New("alarmd evaluation: named input Plan is missing or over budget")
	}
	byLevel := make(map[uint32]execution.SeriesEvaluationInputRequest, len(inputs))
	for _, input := range inputs {
		if input.Contract != header.Contract || input.Consumer.Plan != due.Identity || !input.Consumer.HasLevel ||
			input.SeriesIdentity != first.SeriesIdentity {
			return execution.PlanEvaluationResult{}, errors.New("alarmd evaluation: named inputs span Plan, contract or series")
		}
		if _, duplicate := byLevel[input.Consumer.LevelID]; duplicate {
			return execution.PlanEvaluationResult{}, errors.New("alarmd evaluation: duplicate named input Level")
		}
		byLevel[input.Consumer.LevelID] = input
	}
	levels := due.CompiledPlan.Levels()
	if len(byLevel) != len(levels) {
		return execution.PlanEvaluationResult{}, errors.New("alarmd evaluation: named inputs do not exactly cover compiled Levels")
	}
	ordered := make([]execution.SeriesEvaluationInputRequest, len(levels))
	for index, level := range levels {
		if inputs[index].Consumer.LevelID != level.Definition().LevelID {
			return execution.PlanEvaluationResult{}, errors.New("alarmd evaluation: named inputs are not in compiled Level order")
		}
		input, ok := byLevel[level.Definition().LevelID]
		if !ok {
			return execution.PlanEvaluationResult{}, errors.New("alarmd evaluation: named inputs do not exactly cover compiled Levels")
		}
		ordered[index] = input
	}
	primaryRecords, err := commonPrimaryRecords(ordered, e.limits.MaxRecords)
	if err != nil {
		return execution.PlanEvaluationResult{}, err
	}
	if len(primaryRecords) == 0 {
		return execution.PlanEvaluationResult{}, errors.New("alarmd evaluation: named-input series has no PRIMARY record")
	}
	prepared, err := e.detect.PreparePlan(due.CompiledPlan)
	if err != nil {
		return execution.PlanEvaluationResult{}, err
	}
	identity := execution.StateKeyIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration, SeriesIdentityDigest: first.SeriesIdentity}
	view, ok := stateResult.Find(identity)
	if !ok {
		return execution.PlanEvaluationResult{}, errors.New("alarmd evaluation: runtime state missing")
	}
	converged, err := guardConvergenceAllowed(view, levels)
	if err != nil {
		return execution.PlanEvaluationResult{}, err
	}
	legacy := execution.EvaluationRequest{Header: header, State: stateResult, Gaps: gaps}
	result := execution.PlanEvaluationResult{Plan: due.Identity, Disposition: execution.PlanDecided, ReasonCode: observability.ReasonNone}
	var final *execution.StateEvaluation
	var events []contract.TriggerEventV1
	var affected []execution.RecordAnchor
	for _, record := range primaryRecords {
		one, runErr := e.evaluateRecordWith(ctx, legacy, due, record, view, converged, func() ([]detect.LevelFact, []detect.ProjectedValue, error) {
			facts, projected, _, detectErr := e.detect.EvaluatePreparedSeriesRecord(ctx, prepared, ordered, record)
			return facts, projected, detectErr
		})
		if runErr != nil {
			return execution.PlanEvaluationResult{}, runErr
		}
		result.LevelOutcomes = append(result.LevelOutcomes, one.outcomes...)
		if one.state != nil {
			view = applyProvisional(view, one.state.Mutation)
			final = one.state
			events = append(events, one.state.Events...)
			affected = append(affected, execution.RecordAnchor{RecordID: record.RecordID(), SourceTime: record.SourceTime()})
		}
	}
	if final != nil {
		mutation := final.Mutation
		mutation.MutationDigest = ""
		mutation.AffectedRecords = affected
		mutation, err = execution.BuildStateMutation(mutation)
		if err != nil {
			return execution.PlanEvaluationResult{}, err
		}
		final.Mutation, final.Events = mutation, events
		result.StateResults = []execution.StateEvaluation{*final}
	}
	if gapMutation, gapErr := planGapRecoveryMutation(legacy, due, len(result.StateResults) > 0); gapErr != nil {
		return execution.PlanEvaluationResult{}, gapErr
	} else if gapMutation != nil {
		result.GuardAfterState = []execution.PlanGapMutation{*gapMutation}
	}
	for _, outcome := range result.LevelOutcomes {
		if outcome.Outcome == execution.LevelOutcomeUnknown || outcome.Outcome == execution.LevelOutcomeTerminal {
			result.Disposition, result.ReasonCode = execution.PlanDecidedDegraded, outcome.ReasonCode
			break
		}
	}
	return result, nil
}

func commonPrimaryRecords(inputs []execution.SeriesEvaluationInputRequest, maxRecords uint64) ([]execution.RecordView, error) {
	var canonical []execution.RecordView
	for _, input := range inputs {
		var primary *execution.DatasetView
		for _, binding := range input.Inputs {
			if binding.Role == execution.InputRolePrimary {
				if primary != nil {
					return nil, errors.New("alarmd evaluation: duplicate PRIMARY named input")
				}
				if binding.Completeness != execution.CompletenessFull || binding.Disposition != execution.AccessAvailable ||
					binding.Dataset == nil || binding.View == nil || !binding.View.Uses(binding.Dataset) {
					return nil, errors.New("alarmd evaluation: PRIMARY named input is not FULL and available")
				}
				primary = binding.View
			}
		}
		if primary == nil {
			return nil, errors.New("alarmd evaluation: PRIMARY named input is missing")
		}
		if uint64(primary.Len()) > maxRecords {
			return nil, errors.New("alarmd evaluation: named-input record budget exceeded")
		}
		records := make([]execution.RecordView, primary.Len())
		for index := range records {
			record, ok := primary.Record(index)
			if !ok || execution.SeriesIdentityDigest(record.DimensionIdentity().Digest) != input.SeriesIdentity {
				return nil, errors.New("alarmd evaluation: PRIMARY record differs from named-input series")
			}
			records[index] = record
		}
		sort.Slice(records, func(i, j int) bool {
			if records[i].SourceTime() == records[j].SourceTime() {
				return records[i].RecordID() < records[j].RecordID()
			}
			return records[i].SourceTime() < records[j].SourceTime()
		})
		if canonical == nil {
			canonical = records
			continue
		}
		if !sameRecords(canonical, records) {
			return nil, errors.New("alarmd evaluation: Level PRIMARY views do not share one exact record set")
		}
	}
	return canonical, nil
}

func sameRecords(left, right []execution.RecordView) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].RecordID() != right[index].RecordID() || left[index].SourceTime() != right[index].SourceTime() ||
			left[index].BusinessID() != right[index].BusinessID() || left[index].ReceivedTime() != right[index].ReceivedTime() ||
			!reflect.DeepEqual(left[index].CollectionTime(), right[index].CollectionTime()) ||
			!reflect.DeepEqual(left[index].DimensionIdentity(), right[index].DimensionIdentity()) ||
			!reflect.DeepEqual(left[index].Values(), right[index].Values()) || !reflect.DeepEqual(left[index].Dimensions(), right[index].Dimensions()) {
			return false
		}
	}
	return true
}

// guardConvergenceAllowed reports, per Level, whether the loaded history
// already forms the required full window at the last processed record, so a
// WARMING or GAPPED Level guard may converge on the next FULL record. A Level
// whose live window still lacks points stays guarded.
func guardConvergenceAllowed(view execution.RuntimeStateView, levels []strategy.CompiledLevel) (map[uint32]bool, error) {
	allowed := make(map[uint32]bool, len(levels))
	requirements := make([]state.LevelRequirement, len(levels))
	for i, level := range levels {
		requirements[i] = state.LevelRequirement{LevelID: level.Definition().LevelID, DetectFingerprint: level.Fingerprints().Detect, RequiredPoints: level.RequiredDetectHistoryPoints(), RetentionPoints: level.StateRequirement().RetentionPoints, EvaluationInterval: time.Duration(level.Trigger().StepSeconds) * time.Second}
	}
	window, err := state.NewWindow(requirements)
	if err != nil {
		return nil, err
	}
	if len(view.History) > 0 {
		if _, err = window.Apply(toStatePoints(view.History)); err != nil {
			return nil, err
		}
	}
	for _, level := range levels {
		current, found := levelState(view, level.Definition().LevelID)
		if !found || current.LastProcessedEventTime <= 0 ||
			(current.HistoryCompleteness != execution.HistoryWarming && current.HistoryCompleteness != execution.HistoryGapped) {
			continue
		}
		history, found := window.History(level.Definition().LevelID)
		if !found {
			continue
		}
		summary := history.Summarize(current.LastProcessedEventTime, level.RequiredDetectHistoryPoints())
		allowed[level.Definition().LevelID] = summary.Completeness == state.HistoryFull
	}
	return allowed, nil
}

func (e *Evaluator) constrainedRecord(request execution.EvaluationRequest, due execution.DuePlan, record execution.RecordView, view execution.RuntimeStateView) (recordResult, error) {
	kind := execution.LevelOutcomeUnknown
	if view.Status == execution.StateDeterministicInvalid {
		kind = execution.LevelOutcomeTerminal
	}
	out := recordResult{}
	for _, l := range due.CompiledPlan.Levels() {
		out.outcomes = append(out.outcomes, execution.LevelOutcome{Plan: due.Identity, LevelID: l.Definition().LevelID, SeriesIdentityDigest: view.Identity.SeriesIdentityDigest, Record: execution.RecordAnchor{RecordID: record.RecordID(), SourceTime: record.SourceTime()}, Outcome: kind, ReasonCode: view.ReasonCode})
	}
	return out, nil
}

type historyView struct {
	state.HistoryView
	completeness string
}

func (h historyView) Summarize(t int64, n uint32) trigger.HistorySummary {
	s := h.HistoryView.Summarize(t, n)
	c := h.completeness
	if c == "" {
		c = string(s.Completeness)
	}
	return trigger.HistorySummary{Completeness: c, WindowStart: s.WindowStart, WindowEnd: s.WindowEnd, ValidPositions: s.ValidPositions, AnomalyCount: s.AnomalyCount, AnomalyDigest: s.AnomalyDigest}
}
func levelState(v execution.RuntimeStateView, id uint32) (execution.RuntimeLevelStateView, bool) {
	for _, l := range v.Levels {
		if l.LevelID == id {
			return l, true
		}
	}
	return execution.RuntimeLevelStateView{}, false
}
func gapCompleteness(gaps execution.GapLoadResult, due execution.DuePlan, levelID uint32) (string, execution.ReasonCode, bool) {
	gap, ok := gaps.Find(execution.PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration})
	if !ok || gap.Status != execution.GapFound {
		return "", "", false
	}
	for _, scope := range gap.Scopes {
		if !scope.Scope.HasLevel || scope.Scope.LevelID == levelID {
			if scope.Status == execution.GapStatusGapped {
				return trigger.HistoryGapped, scope.ReasonCode, true
			}
			return trigger.HistoryWarming, scope.ReasonCode, true
		}
	}
	return "", "", false
}
func applyProvisional(view execution.RuntimeStateView, mutation execution.StateMutation) execution.RuntimeStateView {
	view.BlobRevision = mutation.ExpectedBlobRevision
	view.History = append([]execution.StateHistoryPoint(nil), mutation.Points...)
	view.Levels = make([]execution.RuntimeLevelStateView, len(mutation.Levels))
	for i, l := range mutation.Levels {
		view.Levels[i] = execution.RuntimeLevelStateView{LevelID: l.LevelID, LevelStateCompatibility: l.LevelStateCompatibility, HistoryCompleteness: l.HistoryCompleteness, GapReasonCode: l.GapReasonCode, WarmupRequirementRef: l.WarmupRequirementRef, LastProcessedEventTime: l.LastProcessedEventTime}
	}
	view.SeriesGuard = mutation.SeriesGuard
	return view
}
func effectiveFact(h execution.InternalExecutionHeader, p execution.PlanIdentity, l uint32, s execution.SeriesIdentityDigest) (strategy.EffectiveTimeFact, bool) {
	for _, f := range h.EffectiveTimeFacts {
		if f.Consumer.Plan == p && f.Consumer.LevelID == l && f.SeriesIdentity == s {
			return f.Fact, true
		}
	}
	return strategy.EffectiveTimeFact{}, false
}
func toStatePoints(in []execution.StateHistoryPoint) []state.StatePoint {
	out := make([]state.StatePoint, len(in))
	for i, p := range in {
		out[i] = state.StatePoint{RecordID: p.RecordID, SourceTime: p.SourceTime, Levels: make([]state.PointLevelFact, len(p.Levels))}
		for j, l := range p.Levels {
			out[i].Levels[j] = state.PointLevelFact{LevelID: l.LevelID, DetectFingerprint: l.DetectFingerprint, Result: stateFact(string(l.Result))}
		}
	}
	return out
}
func stateFact(v string) state.LevelFactResult {
	switch v {
	case detect.FactResultAnomalous:
		return state.LevelFactAnomalous
	case detect.FactResultNormal:
		return state.LevelFactNormal
	case detect.FactResultError:
		return state.LevelFactError
	default:
		return state.LevelFactUnavailable
	}
}

// guardStaysActive reports whether the durable Level guard survives this
// record: the Level does not advance State, or it advances with a WARMING or
// GAPPED history, which buildMutation writes under the guard reason. The
// completeness derivation mirrors buildMutation exactly.
func guardStaysActive(outcome trigger.LevelOutcomeV2, summary execution.HistoryCompleteness) bool {
	if outcome.StateDisposition != trigger.StateAdvance {
		return true
	}
	completeness := summary
	if completeness == "" {
		completeness = execution.HistoryWarming
	}
	if outcome.HistoryCompleteness != "" {
		completeness = execution.HistoryCompleteness(outcome.HistoryCompleteness)
	}
	return completeness == execution.HistoryWarming || completeness == execution.HistoryGapped
}

func buildMutation(request execution.EvaluationRequest, due execution.DuePlan, record execution.RecordView, view execution.RuntimeStateView, facts []detect.LevelFact, outcomes []trigger.LevelOutcomeV2, summaries map[uint32]execution.HistoryCompleteness, durableGuardReasons map[uint32]execution.ReasonCode) (execution.StateMutation, error) {
	refs, err := execution.DeriveRuntimeLevelContractRefs(due.CompiledPlan)
	if err != nil {
		return execution.StateMutation{}, err
	}
	levels := make([]execution.RuntimeLevelStateMutation, len(refs))
	for i, r := range refs {
		advances := false
		for _, outcome := range outcomes {
			if outcome.LevelID == r.LevelID && outcome.StateDisposition == trigger.StateAdvance {
				advances = true
				break
			}
		}
		if !advances {
			if current, found := levelState(view, r.LevelID); found {
				levels[i] = execution.RuntimeLevelStateMutation{LevelID: r.LevelID, LevelStateCompatibility: r.LevelStateCompatibility, HistoryCompleteness: current.HistoryCompleteness, GapReasonCode: current.GapReasonCode, WarmupRequirementRef: r.WarmupRequirementRef, LastProcessedEventTime: current.LastProcessedEventTime}
				continue
			}
			levels[i] = execution.RuntimeLevelStateMutation{LevelID: r.LevelID, LevelStateCompatibility: r.LevelStateCompatibility, HistoryCompleteness: execution.HistoryWarming, GapReasonCode: execution.ReasonCode(contract.ReasonHistoryWarming), WarmupRequirementRef: r.WarmupRequirementRef}
			continue
		}
		completeness := summaries[r.LevelID]
		if completeness == "" {
			completeness = execution.HistoryWarming
		}
		var reason execution.ReasonCode
		for _, outcome := range outcomes {
			if outcome.LevelID == r.LevelID && outcome.HistoryCompleteness != "" {
				completeness = execution.HistoryCompleteness(outcome.HistoryCompleteness)
				if completeness == execution.HistoryWarming {
					reason = execution.ReasonCode(contract.ReasonHistoryWarming)
				} else if completeness == execution.HistoryGapped {
					reason = execution.ReasonCode(contract.ReasonHistoryGapped)
				}
			}
		}
		if guarded, found := durableGuardReasons[r.LevelID]; found &&
			(completeness == execution.HistoryWarming || completeness == execution.HistoryGapped) {
			reason = guarded
		}
		levels[i] = execution.RuntimeLevelStateMutation{LevelID: r.LevelID, LevelStateCompatibility: r.LevelStateCompatibility, HistoryCompleteness: completeness, GapReasonCode: reason, WarmupRequirementRef: r.WarmupRequirementRef, LastProcessedEventTime: record.SourceTime()}
	}
	advancing := make(map[uint32]bool, len(outcomes))
	for _, outcome := range outcomes {
		advancing[outcome.LevelID] = outcome.StateDisposition == trigger.StateAdvance
	}
	lf := make([]execution.StateLevelFact, 0, len(facts))
	for _, f := range facts {
		if advancing[f.Definition.LevelID] {
			lf = append(lf, execution.StateLevelFact{LevelID: f.Definition.LevelID, DetectFingerprint: f.DetectFingerprint, Result: execution.LevelFactResult(f.Result)})
		}
	}
	points := append(append([]execution.StateHistoryPoint(nil), view.History...), execution.StateHistoryPoint{RecordID: record.RecordID(), SourceTime: record.SourceTime(), Levels: lf})
	var retain uint32
	for _, l := range due.CompiledPlan.Levels() {
		if l.StateRequirement().RetentionPoints > retain {
			retain = l.StateRequirement().RetentionPoints
		}
	}
	if uint32(len(points)) > retain {
		points = points[len(points)-int(retain):]
	}
	version, err := execution.BuildApplyVersion(request.Header.Contract, due.StateApplyEpoch)
	if err != nil {
		return execution.StateMutation{}, err
	}
	return execution.BuildStateMutation(execution.StateMutation{Identity: view.Identity, ExpectedBlobRevision: view.BlobRevision, ApplyVersion: version, AffectedRecords: []execution.RecordAnchor{{RecordID: record.RecordID(), SourceTime: record.SourceTime()}}, Levels: levels, Points: points})
}
