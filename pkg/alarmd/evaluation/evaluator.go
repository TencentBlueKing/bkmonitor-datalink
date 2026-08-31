package evaluation

import (
	"context"
	"errors"
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
	if e == nil || request.Batch.Dataset == nil || uint64(request.Batch.Dataset.Len()) > e.limits.MaxRecords {
		return execution.EvaluationResult{}, errors.New("alarmd evaluation: invalid or over-budget request")
	}
	selected := map[execution.PlanIdentity]bool{}
	for _, b := range request.Batch.Inputs {
		selected[b.Consumer.Plan] = true
	}
	if uint64(len(selected)) > e.limits.MaxPlans {
		return execution.EvaluationResult{}, errors.New("alarmd evaluation: plan budget exceeded")
	}
	result := execution.EvaluationResult{Contract: request.Header.Contract, Result: observability.ResultSuccess, ReasonCode: observability.ReasonNone}
	for _, due := range request.Header.DuePlans {
		if !selected[due.Identity] {
			continue
		}
		planResult, err := e.evaluatePlan(ctx, request, due)
		if err != nil {
			return execution.EvaluationResult{}, err
		}
		result.Plans = append(result.Plans, planResult)
		if planResult.Disposition == execution.PlanDecidedDegraded {
			result.Result = observability.ResultDegraded
			result.ReasonCode = planResult.ReasonCode
		}
	}
	if err := result.Validate(request); err != nil {
		return execution.EvaluationResult{}, err
	}
	return result, nil
}

func (e *Evaluator) evaluatePlan(ctx context.Context, request execution.EvaluationRequest, due execution.DuePlan) (execution.PlanEvaluationResult, error) {
	if uint64(len(due.CompiledPlan.Levels())) > e.limits.MaxLevels {
		return execution.PlanEvaluationResult{}, errors.New("alarmd evaluation: level budget exceeded")
	}
	prepared, err := e.detect.PreparePlan(due.CompiledPlan)
	if err != nil {
		return execution.PlanEvaluationResult{}, err
	}
	result := execution.PlanEvaluationResult{Plan: due.Identity, Disposition: execution.PlanDecided, ReasonCode: observability.ReasonNone}
	type group struct {
		identity execution.StateKeyIdentity
		records  []execution.RecordView
	}
	groups := map[execution.StateKeyIdentity]*group{}
	for _, binding := range request.Batch.Inputs {
		if binding.Consumer.Plan != due.Identity || binding.Role != execution.InputRolePrimary {
			continue
		}
		if binding.Completeness != execution.CompletenessFull {
			return execution.PlanEvaluationResult{}, errors.New("alarmd evaluation: G1 requires FULL input")
		}
		for i := 0; i < binding.View.Len(); i++ {
			record, ok := binding.View.Record(i)
			if !ok {
				return execution.PlanEvaluationResult{}, errors.New("alarmd evaluation: selected record missing")
			}
			identity := execution.StateKeyIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration, SeriesIdentityDigest: execution.SeriesIdentityDigest(record.DimensionIdentity().Digest)}
			current := groups[identity]
			if current == nil {
				current = &group{identity: identity}
				groups[identity] = current
			}
			current.records = append(current.records, record)
		}
	}
	for _, current := range groups {
		sort.Slice(current.records, func(i, j int) bool {
			if current.records[i].SourceTime() == current.records[j].SourceTime() {
				return current.records[i].RecordID() < current.records[j].RecordID()
			}
			return current.records[i].SourceTime() < current.records[j].SourceTime()
		})
		view, ok := request.State.Find(current.identity)
		if !ok {
			return execution.PlanEvaluationResult{}, errors.New("alarmd evaluation: runtime state missing")
		}
		warmingConvergence, err := warmingConvergenceAllowed(view, due.CompiledPlan.Levels())
		if err != nil {
			return execution.PlanEvaluationResult{}, err
		}
		var final *execution.StateEvaluation
		var events []contract.TriggerEventV1
		var affected []execution.RecordAnchor
		for _, record := range current.records {
			one, err := e.evaluateRecord(ctx, request, due, prepared, record, view, warmingConvergence)
			if err != nil {
				return execution.PlanEvaluationResult{}, err
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
			final.Mutation = mutation
			final.Events = events
			result.StateResults = append(result.StateResults, *final)
		}
	}
	gapMutation, err := planGapRecoveryMutation(request, due, len(result.StateResults) > 0)
	if err != nil {
		return execution.PlanEvaluationResult{}, err
	}
	if gapMutation != nil {
		result.GuardAfterState = []execution.PlanGapMutation{*gapMutation}
	}
	for _, outcome := range result.LevelOutcomes {
		if outcome.Outcome == execution.LevelOutcomeUnknown || outcome.Outcome == execution.LevelOutcomeTerminal {
			result.Disposition = execution.PlanDecidedDegraded
			result.ReasonCode = outcome.ReasonCode
			break
		}
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

func (e *Evaluator) evaluateRecord(ctx context.Context, request execution.EvaluationRequest, due execution.DuePlan, prepared detect.PreparedPlan, record execution.RecordView, view execution.RuntimeStateView, warmingConvergence map[uint32]bool) (recordResult, error) {
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
	facts, projected, _, err := e.detect.EvaluatePreparedRecord(ctx, prepared, record)
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
	activeHistoryGuardReasons := make(map[uint32]execution.ReasonCode, len(levels))
	effective := make([]trigger.LevelEffectiveTimeFact, len(levels))
	for i, l := range levels {
		h, _ := window.History(l.Definition().LevelID)
		completeness := ""
		if current, found := levelState(view, l.Definition().LevelID); found {
			if current.HistoryCompleteness == execution.HistoryGapped ||
				(current.HistoryCompleteness == execution.HistoryWarming && !warmingConvergence[l.Definition().LevelID]) {
				completeness = string(current.HistoryCompleteness)
				if current.GapReasonCode != "" {
					activeHistoryGuardReasons[l.Definition().LevelID] = current.GapReasonCode
				}
			}
		}
		if guarded, reason, found := gapCompleteness(request.Gaps, due, l.Definition().LevelID); found {
			completeness = guarded
			activeHistoryGuardReasons[l.Definition().LevelID] = reason
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
			if o.HistoryCompleteness != "" {
				if guarded, found := activeHistoryGuardReasons[o.LevelID]; found {
					reason = guarded
				}
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
		mutation, err := buildMutation(request, due, record, view, facts, tr.LevelOutcomes, historyCompleteness, activeHistoryGuardReasons)
		if err != nil {
			return recordResult{}, err
		}
		result.state = &execution.StateEvaluation{Mutation: mutation, Events: events}
	}
	return result, nil
}

func warmingConvergenceAllowed(view execution.RuntimeStateView, levels []strategy.CompiledLevel) (map[uint32]bool, error) {
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
		if !found || current.HistoryCompleteness != execution.HistoryWarming || current.LastProcessedEventTime <= 0 {
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
func buildMutation(request execution.EvaluationRequest, due execution.DuePlan, record execution.RecordView, view execution.RuntimeStateView, facts []detect.LevelFact, outcomes []trigger.LevelOutcomeV2, summaries map[uint32]execution.HistoryCompleteness, activeHistoryGuardReasons map[uint32]execution.ReasonCode) (execution.StateMutation, error) {
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
		if guarded, found := activeHistoryGuardReasons[r.LevelID]; found &&
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
