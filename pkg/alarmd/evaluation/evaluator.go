package evaluation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"

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
	detect  *detect.Evaluator
	limits  Limits
	samples *observability.SeriesSampler
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
	plan, err := e.evaluateSeries(ctx, request.Header, request.Inputs, request.State, request.Gaps, request.OpenAlerts)
	if err != nil {
		return execution.EvaluationResult{}, err
	}
	result := execution.EvaluationResult{Contract: request.Header.Contract, Plans: []execution.PlanEvaluationResult{plan},
		Result: observability.ResultSuccess, ReasonCode: observability.ReasonNone}
	switch plan.Disposition {
	case execution.PlanDecided:
	case execution.PlanDecidedDegraded, execution.PlanUnavailable, execution.PlanReadinessGap:
		result.Result, result.ReasonCode = observability.ResultDegraded, plan.ReasonCode
		// A Plan degraded by a Level that ended TERMINAL reports TERMINAL in
		// the aggregate, whatever its disposition says. That is the contract's
		// rule and not a second opinion about it: a localized terminal outcome
		// sets the expected aggregate to TERMINAL there, and a Plan reporting
		// DEGRADED beside one was refused for disagreeing with itself.
		for _, outcome := range plan.LevelOutcomes {
			if outcome.Outcome == execution.LevelOutcomeTerminal {
				result.Result = observability.Result(observability.ResultTerminal)
				break
			}
		}
	case execution.PlanTerminal:
		result.Result, result.ReasonCode = observability.ResultTerminal, plan.ReasonCode
	case execution.PlanRetryPending:
		result.Result, result.ReasonCode = observability.ResultRetrying, plan.ReasonCode
	default:
		return execution.EvaluationResult{}, errors.New("alarmd evaluation: invalid named-input Plan result")
	}
	return result, nil
}

// planGapRecoveryMutation is this Slot's recovery of the Plan's marker, for a
// Slot that evaluated series. The gate is what a series-bearing Slot has to
// show for itself: a state mutation, meaning at least one series carried its
// round forward. A Slot with no series at all never reaches here at all -- the
// worker's Slot wrap-up decides that one, under the Plan-scope reach.
//
// The arithmetic is not repeated here. Both callers ask
// execution.PlanGapRecoveryMutation, and differ only in the reach they pass,
// so "how far does one healthy Slot move a warmup count" has one definition.
func planGapRecoveryMutation(
	request execution.EvaluationRequest,
	due execution.DuePlan,
	hasStateMutation bool,
) (*execution.PlanGapMutation, error) {
	if !hasStateMutation {
		return nil, nil
	}
	return execution.PlanGapRecoveryMutation(request.Header.Contract, due, request.Gaps, execution.GapRecoverEveryScope)
}

type recordResult struct {
	outcomes []execution.LevelOutcome
	state    *execution.StateEvaluation
	// gate is what became of a RECOVERY record's envelope; zero for every
	// other record.
	gate     trigger.RecoveryGateV2
	coverage execution.HistoryCoverage
}

// countRecoveryGate adds one RECOVERY record to the Plan's counts under the
// state of the other Level it was decided beside; a record decided beside
// none, and every other record, adds nothing.
func countRecoveryGate(counts *execution.RecoveryGateCounts, gate trigger.RecoveryGateV2) {
	switch gate.Beside {
	case trigger.RecoveryBesideLevelUnavailable:
		counts.BesideLevelUnavailable++
	case trigger.RecoveryBesideLevelRecovering:
		counts.BesideLevelRecovering++
	case trigger.RecoveryBesideLevelWithoutRecovery:
		counts.BesideLevelWithoutRecovery++
	}
}

// countOpenAlertGate adds one record's second-gate outcome to the Plan's
// counts. The outcome is empty for every record the gate was not asked
// about, which is every record that is not RECOVERY.
func countOpenAlertGate(counts *execution.OpenAlertGateCounts, gate trigger.RecoveryGateV2) {
	switch gate.OpenAlertGate {
	case trigger.OpenAlertGatePassed:
		counts.Passed++
	case trigger.OpenAlertGateHeldNoOpenAlert:
		counts.HeldNoOpenAlert++
	case trigger.OpenAlertGateHeldFingerprintUnknown:
		counts.HeldFingerprintUnknown++
	case trigger.OpenAlertGateNotConfigured:
		counts.NotConfigured++
	case trigger.OpenAlertGateProtocolNotGated:
		counts.ProtocolNotGated++
	}
}

type recordDetector func() ([]detect.LevelFact, []detect.ProjectedValue, error)

func (e *Evaluator) evaluateRecordWith(ctx context.Context, request execution.EvaluationRequest, due execution.DuePlan, record execution.RecordView, view execution.RuntimeStateView, guardConvergence map[uint32]bool, sample *observability.SeriesSampleReservation, run recordDetector) (recordResult, error) {
	series := execution.SeriesIdentityDigest(record.DimensionIdentityDigest())
	identity := execution.StateKeyIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration, SeriesIdentityDigest: series}
	if view.Identity != identity {
		return recordResult{}, errors.New("alarmd evaluation: runtime state identity mismatch")
	}
	if view.Status == execution.StateRetryableIO || view.Status == execution.StateDeterministicInvalid {
		return e.constrainedRecord(request, due, record, view)
	}
	levels := due.CompiledPlan.Levels()
	reqs, err := planLevelRequirements(due.CompiledPlan)
	if err != nil {
		return recordResult{}, err
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
	captureSampleFacts(sample, facts, projected)
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
	// Observation only. Summarize is the one place that knows both how much of
	// the window arrived and how much was asked for, and until now it kept both
	// and published neither -- which is why a permanently short window and a
	// window two rounds from converging were indistinguishable everywhere
	// downstream.
	var coverage execution.HistoryCoverage
	// Whether this round found any persisted history for this series, decided
	// once for the record rather than per Level because it is a property of the
	// state key and every Level of one record shares it.
	//
	// It is the only thing in reach that can tell a series whose identity churns
	// from a series whose data is missing. Both hold short windows for ever and
	// report the same completeness every round; the first is new each time it
	// appears and the second is not. Reading it here costs nothing -- the load
	// already happened, and its outcome is on the view being evaluated.
	fresh := view.Status == execution.StateMissingWarming
	// Which Levels could not use this record, by Level, so the window count
	// beside each one can say so. The facts are in hand here and nowhere
	// downstream.
	unusable := make(map[uint32]string, len(facts))
	for _, f := range facts {
		if f.Result == detect.FactResultUnavailable || f.Result == detect.FactResultError {
			unusable[f.Definition.LevelID] = f.ReasonCode
		}
	}
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
		held := historyView{HistoryView: h, completeness: completeness}
		histories[i] = trigger.LevelHistory{LevelID: l.Definition().LevelID, View: held}
		// One walk for the counts and the holes: the summary visits every
		// position and classifies it, and a second walk over a day-long
		// window would double the dearest read on this path for exactly the
		// windows most likely to be short.
		summary, holes := held.summarizeHoles(ctx, record.SourceTime(), l.RequiredDetectHistoryPoints(), execution.MaxWindowHolesListed)
		historyCompleteness[l.Definition().LevelID] = execution.HistoryCompleteness(summary.Completeness)
		// completeness is non-empty exactly when a guard is forcing this Level's
		// verdict, so the same variable that decides what gets reported also says
		// whether it was decided now. It is the only place that knows: by the
		// time the summary is returned the forced value and the computed one are
		// the same field.
		coverage.Observe(summary.ValidPositions, summary.RequiredPositions, completeness != "", fresh)
		if record.SourceTime() > coverage.End {
			coverage.End = record.SourceTime()
		}
		if reason, unusableHere := unusable[l.Definition().LevelID]; unusableHere {
			coverage.ObserveUnusable(reason)
		}
		// The short window by name: which series, and which of its positions
		// are empty -- from the walk above, so the named holes and the
		// shortfall are one count.
		if summary.ValidPositions < summary.RequiredPositions {
			// The guard's reason belongs to a window whose verdict the guard
			// held. A Level loaded WARMING or GAPPED keeps its reason in
			// durableGuardReasons after its guard converges, and from that
			// round the live window decides -- so the reason passed
			// unconditionally described a verdict no guard held, and the
			// reader refused the whole run's coverage under
			// WINDOW_GUARD_REASON_UNGUARDED for it. It fired twenty to thirty
			// times per ten minutes on a running deployment: that many runs
			// reported no coverage at all, on the converging round of a Level
			// whose live window was still short.
			guardReason := execution.ReasonCode("")
			if completeness != "" {
				guardReason = durableGuardReasons[l.Definition().LevelID]
			}
			coverage.ObserveWindow(execution.WindowCoverage{
				Plan: due.Identity, LevelID: l.Definition().LevelID, Series: series,
				Valid: summary.ValidPositions, Required: summary.RequiredPositions, End: record.SourceTime(),
				Missing: holes.Missing, MissingTotal: holes.MissingTotal,
				Unusable: holes.Unusable, UnusableTotal: holes.UnusableTotal,
				Guarded: completeness != "", GuardReason: guardReason, Fresh: fresh,
			})
		}
		fact, found := effectiveFact(request.Header, due.Identity, l.Definition().LevelID, series)
		if !found {
			return recordResult{}, errors.New("alarmd evaluation: EffectiveTime fact missing")
		}
		effective[i] = trigger.LevelEffectiveTimeFact{LevelID: l.Definition().LevelID, Fact: fact}
		if sampled := sample.Level(l.Definition().LevelID); sampled != nil {
			sampled.HistoryCompleteness = summary.Completeness
			sampled.HistoryValid, sampled.HistoryRequired = summary.ValidPositions, summary.RequiredPositions
			sampled.HistoryStart, sampled.HistoryEnd = summary.WindowStart, summary.WindowEnd
			sampled.HistoryForced, sampled.Fresh = completeness != "", fresh
			sampled.EffectiveStatus = string(fact.Status())
		}
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
	tr, err := trigger.EvaluateV2(trigger.EvaluationRequestV2{TenantID: due.Identity.TenantID, BusinessID: due.Identity.BusinessID, Plan: due.CompiledPlan, Record: trigger.DetectionRecord{RecordID: record.RecordID(), SourceTime: record.SourceTime(), ProjectedValues: tvalues, LevelFacts: tfacts}, RecordRef: contract.TriggerRecordRefV1{RecordID: record.RecordID(), SourceTime: record.SourceTime(), DimensionIdentityDigest: string(series), Dimensions: record.Dimensions()}, Observed: contract.TriggerObservedV1{Values: record.Values()}, Histories: histories, EffectiveTimeFacts: effective, EvaluationTime: int64(request.Header.Contract.Slot.EvaluationTime), ExecutionID: request.Header.ExecutionID, Limits: e.limits.Trigger, OpenAlerts: request.OpenAlerts})
	if err != nil {
		return recordResult{}, err
	}
	outcomes := make([]execution.LevelOutcome, len(tr.LevelOutcomes))
	// A FULL query may legitimately contain no historical point for this
	// series. Detection then freezes business evaluation, but its UNKNOWN
	// outcome still needs an exact durable Level guard. Existing guards keep
	// their original reason; schedule suppression alone never creates a gap.
	missingInputGuards := make(map[uint32]execution.ReasonCode)
	for _, outcome := range tr.LevelOutcomes {
		if outcome.UnavailableReason == "" {
			continue
		}
		if _, guarded := durableGuardReasons[outcome.LevelID]; guarded {
			continue
		}
		for _, fact := range facts {
			if fact.Definition.LevelID == outcome.LevelID && fact.Result == detect.FactResultUnavailable && fact.ReasonCode == outcome.UnavailableReason {
				missingInputGuards[outcome.LevelID] = execution.ReasonCode(fact.ReasonCode)
				break
			}
		}
	}
	for i, o := range tr.LevelOutcomes {
		kind := execution.LevelOutcomeKind(o.Result)
		reason := execution.ReasonCode(observability.ReasonNone)
		// A recovery reached on a round whose own inputs were incomplete is
		// held, and carries the reason of the guard this round proposes.
		//
		// decision-022 relaxed one gate and only one: an incomplete *history*
		// no longer stands in the way of closing what is open, because the
		// recovery walk now reads the positions it actually observed. The
		// completeness of *this round's inputs* is a different question with
		// the same shape, and the trigger cannot see it - it is handed facts,
		// not the bindings they were detected from. The newest position the
		// walk counts is this record's own fact, so a fact detected on a
		// dependency that came back empty would be counted as observed
		// evidence when it is exactly the thing that was not observed.
		//
		// ABNORMAL is deliberately not held here: a degraded input may still
		// have crossed a threshold, and refusing to say so is the one
		// direction of this rule that loses an alert.
		if kind == execution.LevelOutcomeRecovery {
			if folded, proposed := execution.RoundGuardReasonForLevel(
				evaluationBindings(request), due.Identity, o.LevelID,
			); proposed {
				kind, reason = execution.LevelOutcomeUnknown, folded
			}
		}
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
				// Unless this round has incomplete inputs of its own for the
				// Level. Then the guard that ends up covering this outcome is
				// the one this round proposes, carrying the fold of those
				// inputs -- so that is the reason the outcome has to carry,
				// and taking the stored marker's instead is what made the two
				// disagree on every Slot of an already guarded Level.
				//
				// Same function, same inputs, called from the two places the
				// contract compares. The stored reason stays for a round that
				// adds nothing: it is still the reason the guard is up.
				if folded, proposed := execution.RoundGuardReasonForLevel(
					evaluationBindings(request), due.Identity, o.LevelID,
				); proposed {
					reason = folded
				}
			}
		}
		outcomes[i] = execution.LevelOutcome{Plan: due.Identity, LevelID: o.LevelID, SeriesIdentityDigest: series, Record: execution.RecordAnchor{RecordID: record.RecordID(), SourceTime: record.SourceTime()}, Outcome: kind, ReasonCode: reason,
			// The result contract expects one envelope per record with a
			// business outcome. A record the recovery gate held has RECOVERY
			// outcomes and no envelope, and says so on each of them.
			EnvelopeHeld: tr.RecoveryGate.Held && kind == execution.LevelOutcomeRecovery}
	}
	// A Level the trigger would advance while its outcome is UNKNOWN records
	// a business fact into a history the guard has not yet released. The
	// result contract admits that only when every input of the Level was
	// complete: a fact detected on a missing or partial dependency must not
	// become the history a warm-up completes on. The trigger sees facts, not
	// inputs, so the Level is held back here, by the contract's own
	// predicate, and stays frozen for this record: no fact, no advance, its
	// guard as it was.
	for i, outcome := range tr.LevelOutcomes {
		if outcome.StateDisposition != trigger.StateAdvance || outcomes[i].Outcome != execution.LevelOutcomeUnknown {
			continue
		}
		if !execution.InputAllowsStateAdvance(evaluationBindings(request), outcomes[i]) {
			tr.LevelOutcomes[i].StateDisposition = trigger.StateFreeze
		}
	}
	advance := false
	for _, outcome := range tr.LevelOutcomes {
		if outcome.StateDisposition == trigger.StateAdvance {
			advance = true
		}
		// The verdict and the window it was reached on, side by side. The
		// trigger does not carry completeness on an ABNORMAL outcome -- it
		// decides that result before it reads the summary -- so it is taken
		// from the map this function filled when it summarised the Level.
		if outcome.Result == contract.LevelResultAbnormal {
			coverage.Abnormal++
			if historyCompleteness[outcome.LevelID] != execution.HistoryFull {
				coverage.AbnormalOnIncomplete++
			}
		}
	}
	events, withoutMessage := keptEvents(tr.TriggerEvent)
	result := recordResult{outcomes: outcomes, gate: tr.RecoveryGate, coverage: coverage}
	if advance || len(missingInputGuards) > 0 {
		mutation, err := buildMutation(request, due, record, view, facts, tr.LevelOutcomes, historyCompleteness, durableGuardReasons, missingInputGuards)
		if err != nil {
			return recordResult{}, err
		}
		result.state = &execution.StateEvaluation{Mutation: mutation, Events: events, WithoutMessage: withoutMessage}
	}
	captureSampleDecision(sample, tr)
	return result, nil
}

// keptEvents is what the series keeps of the event its record decided: the
// event, or - when the sink would take it and send nothing - its identity
// only. The event is built either way, so what it is and whether it builds
// are unchanged; what changes is that one the sink would drop is garbage from
// here rather than from the sink, and it was the largest thing the Slot held
// for such a series.
func keptEvents(event *contract.TriggerEventV1) ([]contract.TriggerEventV1, []execution.EventWithoutMessage) {
	switch {
	case event == nil:
		return nil, nil
	case contract.DroppedAtSink(event):
		return nil, []execution.EventWithoutMessage{{
			Record:    execution.RecordAnchor{RecordID: event.RecordRef.RecordID, SourceTime: event.RecordRef.SourceTime},
			EventKind: event.EventKind, Format: contract.OutputWireFormatOf(event),
		}}
	default:
		return []contract.TriggerEventV1{*event}, nil
	}
}

// appendKept adds one record's kept outputs to the series': its events and
// the identities of the events it did not keep. One step for both, because
// the series' mutation carries the two together and a record whose events
// were folded in while its identities were not would lose them from every
// count the write adds back.
func appendKept(events []contract.TriggerEventV1, withoutMessage []execution.EventWithoutMessage, state *execution.StateEvaluation) ([]contract.TriggerEventV1, []execution.EventWithoutMessage) {
	return append(events, state.Events...), append(withoutMessage, state.WithoutMessage...)
}

// evaluateSeries evaluates one due Plan and one series. The slice contains one
// named-input request per compiled Level and must exactly cover the Plan.
func (e *Evaluator) evaluateSeries(
	ctx context.Context,
	header execution.InternalExecutionHeader,
	inputs []execution.SeriesEvaluationInputRequest,
	stateResult execution.StatePreflightResult,
	gaps execution.GapLoadResult,
	openAlerts contract.OpenAlertSet,
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
	if !foundPlan || due.CompiledPlan == nil {
		return execution.PlanEvaluationResult{}, errors.New("alarmd evaluation: named input Plan is missing")
	}
	// Everything below asks the Plan which levels it has, and from here on the
	// Plan is the one this kind of series is judged against. A real series sees
	// the strategy's declared levels; a synthetic no-data series sees the
	// no-data level and nothing else.
	due, err := execution.PlanViewFor(due, first.Kind)
	if err != nil {
		return execution.PlanEvaluationResult{}, err
	}
	if uint64(len(due.CompiledPlan.Levels())) > e.limits.MaxLevels {
		return execution.PlanEvaluationResult{}, errors.New("alarmd evaluation: named input Plan is over budget")
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
	converged, err := guardConvergenceAllowed(view, due.CompiledPlan)
	if err != nil {
		return execution.PlanEvaluationResult{}, err
	}
	// The record evaluator judges a Level's inputs by the same bindings the
	// result contract will, so the series' named inputs travel with the
	// request rather than only its header.
	legacy := execution.EvaluationRequest{Header: header, Inputs: inputs, State: stateResult, Gaps: gaps, OpenAlerts: openAlerts}
	result := execution.PlanEvaluationResult{Plan: due.Identity, Disposition: execution.PlanDecided, ReasonCode: observability.ReasonNone}
	// What the loads decide, before anything is evaluated. A load that failed
	// or is terminal settles the Plan's disposition on its own; the outcome
	// fold at the end only speaks when they did not.
	loaded := dispositionFromLoads(view, gaps, due)
	if loaded.decided {
		result.Disposition, result.ReasonCode = loaded.disposition, loaded.reason
	}
	var final *execution.StateEvaluation
	var events []contract.TriggerEventV1
	var withoutMessage []execution.EventWithoutMessage
	var affected []execution.RecordAnchor
	// The history as the store holds it, kept apart from the provisional view
	// the records build on each other. A Slot with several records advances the
	// view record by record, but the mutation that leaves the Slot describes one
	// write against the record that was loaded, so its base is this one and its
	// points are every point the Slot added.
	baseHistory := view.History
	var delta []execution.StateHistoryPoint
	for recordIndex, record := range primaryRecords {
		if loaded.constrains {
			// The Plan's own gap marker could not be read, or is terminal. The
			// guard state this round would be judged against is unknown, so
			// every series is held where it is and nothing is written: a State
			// advance decided without knowing the guard is the one write that
			// cannot be taken back.
			result.LevelOutcomes = append(result.LevelOutcomes,
				constrainedOutcomes(due, record, first.SeriesIdentity, loaded.outcome, loaded.reason)...)
			continue
		}
		// A sample is reserved only for a record that is going to be evaluated:
		// a constrained round above writes nothing and has nothing to sample.
		var sample *observability.SeriesSampleReservation
		if recordIndex == 0 && e.samples != nil {
			sample = e.reserveSeriesSample(ctx, header, due, ordered, record, view)
		}
		one, runErr := e.evaluateRecordWith(ctx, legacy, due, record, view, converged, sample, func() ([]detect.LevelFact, []detect.ProjectedValue, error) {
			facts, projected, _, detectErr := e.detect.EvaluatePreparedSeriesRecord(ctx, prepared, ordered, record)
			return facts, projected, detectErr
		})
		if runErr != nil {
			sample.Cancel()
			return execution.PlanEvaluationResult{}, runErr
		}
		finishSeriesSample(sample, one)
		result.LevelOutcomes = append(result.LevelOutcomes, one.outcomes...)
		countRecoveryGate(&result.RecoveryGate, one.gate)
		countOpenAlertGate(&result.OpenAlertGate, one.gate)
		result.HistoryCoverage.Merge(one.coverage)
		if one.state != nil {
			for _, point := range one.state.Mutation.Points {
				if delta, err = appendHistoryPoint(delta, point); err != nil {
					return execution.PlanEvaluationResult{}, err
				}
			}
			// Only when another record of this Slot will read it. The view is
			// the merged window, and materializing one per record of a Slot
			// that has a single record puts back the per-round allocation this
			// mutation shape exists to remove.
			if recordIndex+1 < len(primaryRecords) {
				if view, err = applyProvisional(view, one.state.Mutation); err != nil {
					return execution.PlanEvaluationResult{}, err
				}
			}
			final = one.state
			events, withoutMessage = appendKept(events, withoutMessage, one.state)
			affected = append(affected, execution.RecordAnchor{RecordID: record.RecordID(), SourceTime: record.SourceTime()})
		}
	}
	if final != nil {
		mutation := final.Mutation
		mutation.AffectedRecords = affected
		mutation.BaseHistory, mutation.Points = baseHistory, delta
		mutation, err = execution.BuildStateMutation(mutation)
		if err != nil {
			return execution.PlanEvaluationResult{}, err
		}
		final.Mutation, final.Events, final.WithoutMessage = mutation, events, withoutMessage
		result.StateResults = []execution.StateEvaluation{*final}
	}
	if gapMutation, gapErr := planGapRecoveryMutation(legacy, due, len(result.StateResults) > 0); gapErr != nil {
		return execution.PlanEvaluationResult{}, gapErr
	} else if gapMutation != nil {
		result.GuardAfterState = []execution.PlanGapMutation{*gapMutation}
	}
	// The outcome fold, and only when the loads left the disposition alone.
	// It reads what the Levels concluded, which says nothing about whether the
	// records those conclusions came from could be read at all: a series whose
	// State load failed concludes UNKNOWN, and folding that to DECIDED_DEGRADED
	// is how a Plan that has to be retried came to be reported as decided.
	if !loaded.decided {
		for _, outcome := range result.LevelOutcomes {
			if outcome.Outcome == execution.LevelOutcomeUnknown || outcome.Outcome == execution.LevelOutcomeTerminal {
				result.Disposition, result.ReasonCode = execution.PlanDecidedDegraded, outcome.ReasonCode
				break
			}
		}
	}
	return result, nil
}

// loadDisposition is what a Plan's loads settle before anything is evaluated.
//
// constrains says every series of the Plan is held rather than evaluated. That
// is the gap's doing, not the State's: a gap marker that could not be read or
// is terminal leaves the guard state of the whole Plan unknown, and a Level
// whose guard is unknown must not advance. A State load that failed is one
// series' problem -- the others read their own keys and evaluate normally --
// and constrainedRecord already holds that series where it is.
type loadDisposition struct {
	decided     bool
	constrains  bool
	disposition execution.PlanDisposition
	reason      execution.ReasonCode
	outcome     execution.LevelOutcomeKind
}

// dispositionFromLoads reads the two loads this round was handed.
//
// A failed State or gap load is retry-pending: the round can be run again and
// is expected to succeed, and the contract requires that Plan to say so --
// reporting it as decided is a Plan that will not be retried and whose result
// was reached without the state it was supposed to read. A terminal gap marker
// is TERMINAL instead: retrying reads the same broken marker.
//
// The State reason comes first when both loads failed, so the Plan names the
// one closest to what it could not do. Retrying is idempotent either way; a
// stable choice is what keeps the same round reporting the same reason.
func dispositionFromLoads(
	view execution.RuntimeStateView, gaps execution.GapLoadResult, due execution.DuePlan,
) loadDisposition {
	marker, found := gaps.Find(due.GapIdentity())
	if view.Status == execution.StateRetryableIO {
		if found && marker.Status == execution.GapUnavailable {
			// Both failed. The Plan is held whole -- the gap's doing -- and
			// names the State's reason, which is the one this series met first.
			return loadDisposition{decided: true, constrains: true,
				disposition: execution.PlanRetryPending, reason: view.ReasonCode,
				outcome: execution.LevelOutcomeUnknown}
		}
		return loadDisposition{decided: true,
			disposition: execution.PlanRetryPending, reason: view.ReasonCode}
	}
	if !found {
		return loadDisposition{}
	}
	switch marker.Status {
	case execution.GapUnavailable:
		return loadDisposition{decided: true, constrains: true,
			disposition: execution.PlanRetryPending, reason: marker.ReasonCode,
			outcome: execution.LevelOutcomeUnknown}
	case execution.GapTerminal:
		// Retryable outranks terminal wherever both are in play: a round that
		// can succeed on its own should be given the chance, and a terminal
		// marker is still terminal on the next round.
		return loadDisposition{decided: true, constrains: true,
			disposition: execution.PlanTerminal, reason: marker.ReasonCode,
			outcome: execution.LevelOutcomeTerminal}
	default:
		return loadDisposition{}
	}
}

// constrainedOutcomes is one record's Levels held where they are, each naming
// the load that held them.
func constrainedOutcomes(
	due execution.DuePlan, record execution.RecordView, series execution.SeriesIdentityDigest,
	kind execution.LevelOutcomeKind, reason execution.ReasonCode,
) []execution.LevelOutcome {
	outcomes := make([]execution.LevelOutcome, 0, len(due.CompiledPlan.Levels()))
	for _, level := range due.CompiledPlan.Levels() {
		outcomes = append(outcomes, execution.LevelOutcome{
			Plan: due.Identity, LevelID: level.Definition().LevelID, SeriesIdentityDigest: series,
			Record:  execution.RecordAnchor{RecordID: record.RecordID(), SourceTime: record.SourceTime()},
			Outcome: kind, ReasonCode: reason,
		})
	}
	return outcomes
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
			if !ok || execution.SeriesIdentityDigest(record.DimensionIdentityDigest()) != input.SeriesIdentity {
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

// planLevelRequirements is the only place phase two builds the window
// requirements of a Plan. The retention facts come from the one derivation the
// store also writes its TTL from, so a window can never prune to a horizon the
// stored key is allowed to expire inside: building them here a second time,
// with a different interval or lateness tolerance, is exactly what would invert
// that ordering.
func planLevelRequirements(plan *strategy.CompiledPlan) ([]state.LevelRequirement, error) {
	retention, err := execution.DeriveStateRetentionRequirement(plan)
	if err != nil {
		return nil, err
	}
	levels := plan.Levels()
	if len(retention) != len(levels) {
		return nil, errors.New("alarmd evaluation: Plan retention is not aligned with its Levels")
	}
	requirements := make([]state.LevelRequirement, len(levels))
	for index, level := range levels {
		if retention[index].LevelID != level.Definition().LevelID {
			return nil, errors.New("alarmd evaluation: Plan retention is not aligned with its Levels")
		}
		requirements[index] = state.NewLevelRequirement(
			retention[index], level.Fingerprints().Detect, level.RequiredDetectHistoryPoints(),
		)
	}
	return requirements, nil
}

// guardConvergenceAllowed reports, per Level, whether the loaded history
// already forms the required full window at the last processed record, so a
// WARMING or GAPPED Level guard may converge on the next FULL record. A Level
// whose live window still lacks points stays guarded.
func guardConvergenceAllowed(view execution.RuntimeStateView, plan *strategy.CompiledPlan) (map[uint32]bool, error) {
	levels := plan.Levels()
	allowed := make(map[uint32]bool, len(levels))
	requirements, err := planLevelRequirements(plan)
	if err != nil {
		return nil, err
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
	// Counted where the record is turned away, not where the windows are
	// summarised: this is the one place that knows a series produced Level
	// outcomes without an evaluation behind them, and the coverage a reader
	// sees is otherwise silent about it.
	out := recordResult{coverage: execution.HistoryCoverage{Constrained: 1}}
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
	summary, _ := h.summarizeHoles(context.Background(), t, n, 0)
	return summary
}

// summarizeHoles is Summarize with the holes named from the same walk. The
// forced completeness is applied here and only here, so the counts the
// trigger reads and the holes the coverage names are one walk's answer.
func (h historyView) summarizeHoles(ctx context.Context, t int64, n uint32, limit int) (trigger.HistorySummary, state.WindowHoles) {
	s, holes := h.HistoryView.SummarizeHolesContext(ctx, t, n, limit)
	c := h.completeness
	if c == "" {
		c = string(s.Completeness)
	}
	return trigger.HistorySummary{Completeness: c, WindowStart: s.WindowStart, WindowEnd: s.WindowEnd,
		ValidPositions: s.ValidPositions, RequiredPositions: s.RequiredPositions,
		AnomalyCount: s.AnomalyCount, AnomalyDigest: s.AnomalyDigest}, holes
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
	gap, ok := gaps.Find(due.GapIdentity())
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

// applyProvisional is the window the next record of this Slot reads: what the
// store would hold if this mutation landed. It materializes the merge, which
// is why the caller only asks for it when there is a next record.
func applyProvisional(view execution.RuntimeStateView, mutation execution.StateMutation) (execution.RuntimeStateView, error) {
	merged, err := execution.MergedHistory(mutation.BaseHistory, mutation.Points, mutation.RetentionPoints)
	if err != nil {
		return execution.RuntimeStateView{}, err
	}
	view.BlobRevision = mutation.ExpectedBlobRevision
	view.History = merged
	view.Levels = make([]execution.RuntimeLevelStateView, len(mutation.Levels))
	for i, l := range mutation.Levels {
		view.Levels[i] = execution.RuntimeLevelStateView{LevelID: l.LevelID, LevelStateCompatibility: l.LevelStateCompatibility, HistoryCompleteness: l.HistoryCompleteness, GapReasonCode: l.GapReasonCode, WarmupRequirementRef: l.WarmupRequirementRef, LastProcessedEventTime: l.LastProcessedEventTime}
	}
	view.SeriesGuard = mutation.SeriesGuard
	return view, nil
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

// evaluationBindings is every named input the request binds, across its
// series requests, which is the set the result contract judges a Level's
// inputs by; the predicate filters it to one Plan and Level itself.
func evaluationBindings(request execution.EvaluationRequest) []execution.NamedInputBinding {
	var bindings []execution.NamedInputBinding
	for _, input := range request.Inputs {
		bindings = append(bindings, input.Inputs...)
	}
	return bindings
}

func buildMutation(request execution.EvaluationRequest, due execution.DuePlan, record execution.RecordView, view execution.RuntimeStateView, facts []detect.LevelFact, outcomes []trigger.LevelOutcomeV2, summaries map[uint32]execution.HistoryCompleteness, durableGuardReasons, missingInputGuards map[uint32]execution.ReasonCode) (execution.StateMutation, error) {
	refs, err := execution.LevelContractRefsFor(due, view.Levels)
	if err != nil {
		return execution.StateMutation{}, err
	}
	levels := make([]execution.RuntimeLevelStateMutation, len(refs))
	for i, r := range refs {
		if reason, missing := missingInputGuards[r.LevelID]; missing {
			levels[i] = execution.RuntimeLevelStateMutation{LevelID: r.LevelID, LevelStateCompatibility: r.LevelStateCompatibility, HistoryCompleteness: execution.HistoryGapped, GapReasonCode: reason, WarmupRequirementRef: r.WarmupRequirementRef, LastProcessedEventTime: record.SourceTime()}
			continue
		}
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
		for _, outcome := range outcomes {
			if outcome.LevelID == r.LevelID && outcome.HistoryCompleteness != "" {
				completeness = execution.HistoryCompleteness(outcome.HistoryCompleteness)
			}
		}
		// The reason follows the completeness, whichever of the two decided
		// it: the trigger's outcome when it evaluated the Level, the summary
		// alone when it did not. A Level suppressed by its effective time
		// still advances its history, and the trigger returns before it
		// says anything about the window; deriving the reason only from what
		// the trigger said left such a Level WARMING with no reason, which
		// the state contract refuses - every Slot of every strategy outside
		// its hours, from the first one whose window was not yet full.
		var reason execution.ReasonCode
		switch completeness {
		case execution.HistoryWarming:
			reason = execution.ReasonCode(contract.ReasonHistoryWarming)
		case execution.HistoryGapped:
			reason = execution.ReasonCode(contract.ReasonHistoryGapped)
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
		_, missing := missingInputGuards[f.Definition.LevelID]
		if advancing[f.Definition.LevelID] || missing {
			lf = append(lf, execution.StateLevelFact{LevelID: f.Definition.LevelID, DetectFingerprint: f.DetectFingerprint, Result: execution.LevelFactResult(f.Result)})
		}
	}
	point, err := deltaHistoryPoint(view.History, execution.StateHistoryPoint{
		RecordID: record.RecordID(), SourceTime: record.SourceTime(), Levels: lf})
	if err != nil {
		return execution.StateMutation{}, err
	}
	retain := planRetentionPoints(due)
	version, versionErr := execution.BuildApplyVersion(request.Header.Contract, due.StateApplyEpoch)
	if versionErr != nil {
		return execution.StateMutation{}, versionErr
	}
	// Provisional: only the mutation that survives the series is digested, by
	// evaluateSeries, once its full affected-record set is known.
	//
	// One point, not the window. The record this write leaves behind is the
	// loaded history with this point merged in, bounded by the retention, and
	// the store builds it while it serializes - see execution.WalkMergedHistory.
	return execution.BuildProvisionalStateMutation(execution.StateMutation{Identity: view.Identity, ExpectedBlobRevision: view.BlobRevision, ApplyVersion: version, AffectedRecords: []execution.RecordAnchor{{RecordID: record.RecordID(), SourceTime: record.SourceTime()}}, Levels: levels, Points: []execution.StateHistoryPoint{point}, RetentionPoints: retain, BaseHistory: view.History})
}

// planRetentionPoints is the bound in force for this Plan's record: the
// largest any of its Levels asks for, since one record holds every Level's
// window. Derived here and asserted equal by the result contract, which reads
// the compiled Plan the same way, so the mutation cannot carry a bound the
// Plan does not ask for.
func planRetentionPoints(due execution.DuePlan) uint32 {
	var retain uint32
	if due.CompiledPlan == nil {
		return 0
	}
	for _, l := range due.CompiledPlan.Levels() {
		if l.StateRequirement().RetentionPoints > retain {
			retain = l.StateRequirement().RetentionPoints
		}
	}
	return retain
}

// appendHistoryPoint places a freshly evaluated point into the loaded history
// instead of appending it unconditionally.
//
// The history is validated as strictly increasing by (SourceTime, RecordID),
// equality included, so an unconditional append turns any re-evaluation of a
// record the history already holds into a rejected state mutation - the process
// reports its own write as invalid. That is what a restart produces: a Slot
// evaluates a record and writes the state, the process stops before the Slot is
// recorded as finished, and the record is evaluated once more on the way back.
// The observed error was "state history points must be uniquely ordered", every
// occurrence inside a restart window, clearing on its own once the window
// passed.
//
// The rule is the one state/window.go already applies to the live window: a
// point that shares a SourceTime with a stored point is the same point, so the
// Level facts merge and no second point appears. Two different records claiming
// one SourceTime are a record identity conflict, which is named rather than
// left to surface as a broken invariant. Anything else keeps its position by
// SourceTime, so a record that arrives out of order lands where it belongs.
func appendHistoryPoint(history []execution.StateHistoryPoint, point execution.StateHistoryPoint) ([]execution.StateHistoryPoint, error) {
	placed, err := deltaHistoryPoint(history, point)
	if err != nil {
		return nil, err
	}
	return execution.MergedHistory(history, []execution.StateHistoryPoint{placed}, 0)
}

// deltaHistoryPoint is the same rule stated as one point rather than a window:
// what this round adds at this source time, which is the fresh point itself
// unless the history already holds that position, in which case it is the
// stored facts and the fresh ones together.
//
// The mutation carries this and the store merges it back, so the disagreement
// between a stored fact and a fresh one for the same record has to be named
// here - the store sees the merged point and can no longer tell that two
// evaluations of one record disagreed about a Level.
func deltaHistoryPoint(history []execution.StateHistoryPoint, point execution.StateHistoryPoint) (execution.StateHistoryPoint, error) {
	position := sort.Search(len(history), func(index int) bool { return history[index].SourceTime >= point.SourceTime })
	if position == len(history) || history[position].SourceTime != point.SourceTime {
		return point, nil
	}
	if history[position].RecordID != point.RecordID {
		return execution.StateHistoryPoint{}, &namedEvaluationError{code: "STATE_RECORD_IDENTITY_CONFLICT",
			err: fmt.Errorf("alarmd evaluation: record identity conflict at source time %d", point.SourceTime)}
	}
	levels, err := mergeLevelFacts(history[position].Levels, point.Levels)
	if err != nil {
		return execution.StateHistoryPoint{}, err
	}
	point.Levels = levels
	return point, nil
}

// mergeLevelFacts keeps one fact per Level. A Level the stored point already
// carries must agree with the fresh evaluation of the same record: the same
// record under the same Level cannot be both anomalous and not, and silently
// preferring either side would make the state depend on how many times the
// record happened to be evaluated.
func mergeLevelFacts(stored, fresh []execution.StateLevelFact) ([]execution.StateLevelFact, error) {
	merged := append([]execution.StateLevelFact(nil), stored...)
	byLevel := make(map[uint32]int, len(merged))
	for index, fact := range merged {
		byLevel[fact.LevelID] = index
	}
	for _, fact := range fresh {
		index, known := byLevel[fact.LevelID]
		if !known {
			byLevel[fact.LevelID] = len(merged)
			merged = append(merged, fact)
			continue
		}
		if merged[index].Result != fact.Result || merged[index].DetectFingerprint != fact.DetectFingerprint {
			return nil, &namedEvaluationError{code: "STATE_LEVEL_FACT_DISAGREEMENT",
				err: fmt.Errorf("alarmd evaluation: Level %d disagrees with the stored fact for the same record", fact.LevelID)}
		}
	}
	return merged, nil
}

// namedEvaluationError carries the cause's own bounded name out of the
// evaluation. Without it the name of the failure is decided by which wrap site
// the error reached, so everything thrown from one site aggregates into one
// value and the cause is left in the free-text message - the only field that
// says why, and the only one that is rate limited.
//
// The category is deliberately absent: where the failure happened is what the
// wrapping stage knows, and it stays that stage's answer.
type namedEvaluationError struct {
	code string
	err  error
}

func (e *namedEvaluationError) Error() string                  { return e.err.Error() }
func (e *namedEvaluationError) Unwrap() error                  { return e.err }
func (e *namedEvaluationError) QueryFailure() (string, string) { return "", e.code }
