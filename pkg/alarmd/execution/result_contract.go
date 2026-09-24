// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"sort"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

type LevelOutcomeKind string

const (
	LevelOutcomeNormal   LevelOutcomeKind = "NORMAL"
	LevelOutcomeAbnormal LevelOutcomeKind = "ABNORMAL"
	LevelOutcomeRecovery LevelOutcomeKind = "RECOVERY"
	LevelOutcomeUnknown  LevelOutcomeKind = "UNKNOWN"
	LevelOutcomeTerminal LevelOutcomeKind = "TERMINAL"
)

type PartialProofResult string

const PartialProofProvenAbnormal PartialProofResult = "PROVEN_ABNORMAL"

// PartialDecisionProof is a runtime proof receipt for one PARTIAL input. It
// references a finite Compiler-registered proof; it is not a generic theorem
// proof payload.
type PartialDecisionProof struct {
	RequirementID  RequirementID
	DatasetName    DatasetName
	EvidenceDigest string
	Proof          PartialProofRef
	Result         PartialProofResult
}

type LevelOutcome struct {
	Plan                 PlanIdentity
	LevelID              uint32
	SeriesIdentityDigest SeriesIdentityDigest
	Record               RecordAnchor
	Outcome              LevelOutcomeKind
	ReasonCode           ReasonCode
	PartialProofs        []PartialDecisionProof
	// EnvelopeHeld marks a RECOVERY outcome whose record's RECOVERY envelope
	// the open alert gate held back: the consumer holds no open alert on the
	// series, or the identity it keys alerts by could not be built. The
	// outcome is a fact about this Level and still reaches the state. The
	// result contract expects no TriggerEvent for such a record, and only for
	// such a record.
	EnvelopeHeld bool
}

type levelOutcomeIdentity struct {
	Plan                 PlanIdentity
	LevelID              uint32
	SeriesIdentityDigest SeriesIdentityDigest
	Record               RecordAnchor
}

func expectedLevelOutcomeIdentities(input InternalExecution, plan DuePlan) (map[levelOutcomeIdentity]struct{}, error) {
	wanted := make(map[levelOutcomeIdentity]struct{})
	for _, binding := range input.Inputs {
		if binding.Consumer.Plan != plan.Identity || binding.Role != InputRolePrimary {
			continue
		}
		add := func(series SeriesIdentityDigest, anchor RecordAnchor) {
			for _, level := range plan.CompiledPlan.Levels() {
				if binding.Consumer.HasLevel && binding.Consumer.LevelID != level.Definition().LevelID {
					continue
				}
				wanted[levelOutcomeIdentity{
					Plan: plan.Identity, LevelID: level.Definition().LevelID,
					SeriesIdentityDigest: series, Record: anchor,
				}] = struct{}{}
			}
		}
		if binding.Dataset != nil {
			for index := 0; index < binding.View.Len(); index++ {
				record, ok := binding.View.Record(index)
				if !ok {
					return nil, resultContractViolation(codeOutcomePrimaryRecordMissing, "selected PRIMARY record is missing")
				}
				add(SeriesIdentityDigest(record.DimensionIdentityDigest()), RecordAnchor{
					RecordID: record.RecordID(), SourceTime: record.SourceTime(),
				})
			}
		}
		for _, fact := range binding.QualityFacts {
			if fact.ImpactScope == ImpactSeries {
				add(fact.SeriesIdentity, RecordAnchor{RecordID: fact.RecordID, SourceTime: fact.SourceTime})
			}
		}
		for _, terminal := range binding.Terminals {
			if terminal.ImpactScope == ImpactSeries {
				add(terminal.SeriesIdentity, RecordAnchor{RecordID: terminal.RecordID, SourceTime: terminal.SourceTime})
			}
		}
	}
	return wanted, nil
}

func validateLevelOutcomes(
	input InternalExecution,
	plan DuePlan,
	result PlanEvaluationResult,
	states StatePreflightResult,
	gaps GapLoadResult,
) error {
	wanted, err := expectedLevelOutcomeIdentities(input, plan)
	if err != nil {
		return err
	}
	if err := validateStateHistoryReplacements(plan, result.StateResults, states); err != nil {
		return err
	}
	// The markers this round ends with, built the way the exact-guard rule
	// builds them, so an outcome's reason is judged against the guard that
	// will actually be covering it rather than against a second derivation of
	// what that guard ought to say.
	finalMarkers := cloneGapScopes(loadedGapScopes(gaps, result.Plan))
	applyGapMutations(finalMarkers, result.GuardBeforeEvents)
	applyGapMutations(finalMarkers, result.GuardAfterState)
	seen := make(map[levelOutcomeIdentity]LevelOutcome, len(result.LevelOutcomes))
	for _, outcome := range result.LevelOutcomes {
		identity := levelOutcomeIdentity{
			Plan: outcome.Plan, LevelID: outcome.LevelID, SeriesIdentityDigest: outcome.SeriesIdentityDigest, Record: outcome.Record,
		}
		if _, ok := wanted[identity]; !ok {
			return resultContractViolation(codeOutcomeNotASelectedLevel, "Level outcome does not belong to a selected Plan record Level")
		}
		if _, duplicate := seen[identity]; duplicate {
			return resultContractViolation(codeOutcomeDuplicate, "duplicate Level outcome")
		}
		if err := validateLevelOutcome(input, plan, result.Disposition, outcome, result.StateResults, states, gaps, finalMarkers); err != nil {
			return err
		}
		seen[identity] = outcome
	}
	if len(seen) != len(wanted) {
		return resultContractViolation(codeOutcomeMissingForLevel, "every selected Plan record Level requires one outcome")
	}
	if err := validateStateOutcomes(input, plan, result, states, seen); err != nil {
		return err
	}
	return validateEventOutcomes(input, result, seen)
}

func validateLevelOutcome(
	input InternalExecution,
	plan DuePlan,
	disposition PlanDisposition,
	outcome LevelOutcome,
	stateResults []StateEvaluation,
	states StatePreflightResult,
	gaps GapLoadResult,
	finalMarkers map[GapScope]GapScopeState,
) error {
	if outcome.Plan != plan.Identity || !compiledPlanHasLevel(plan.CompiledPlan, outcome.LevelID) ||
		outcome.SeriesIdentityDigest == "" {
		return resultContractViolation(codeOutcomeIdentityIncomplete, "incomplete Level outcome identity")
	}
	if err := outcome.Record.Validate(); err != nil {
		return err
	}
	switch outcome.Outcome {
	case LevelOutcomeNormal, LevelOutcomeAbnormal, LevelOutcomeRecovery:
		if err := ValidateResultReason(observability.ResultSuccess, outcome.ReasonCode); err != nil {
			return err
		}
	case LevelOutcomeUnknown:
		if coverageErr := requireReasonClass(outcome.ReasonCode, contract.ReasonClassCoverage); coverageErr != nil {
			if retryableErr := requireReasonClass(outcome.ReasonCode, contract.ReasonClassRetryable); retryableErr != nil {
				return coverageErr
			}
		}
	case LevelOutcomeTerminal:
		if err := requireReasonClass(outcome.ReasonCode, contract.ReasonClassDeterministic); err != nil {
			return err
		}
	default:
		return resultContractViolation(codeOutcomeKindInvalid, "invalid Level outcome")
	}
	stateIdentity := StateKeyIdentity{Plan: plan.Identity, StateGeneration: plan.StateGeneration, SeriesIdentityDigest: outcome.SeriesIdentityDigest}
	constrained := false
	if view, found := states.Find(stateIdentity); found {
		switch view.Status {
		case StateRetryableIO:
			if outcome.Outcome != LevelOutcomeUnknown || outcome.ReasonCode != view.ReasonCode {
				return resultContractViolation(codeOutcomeRetryableSeriesNotUnknown, "retryable State series requires matching UNKNOWN Level outcome")
			}
			constrained = true
		case StateDeterministicInvalid:
			if outcome.Outcome != LevelOutcomeTerminal || outcome.ReasonCode != view.ReasonCode {
				return resultContractViolation(codeOutcomeInvalidSeriesNotTerminal, "invalid State series requires matching TERMINAL Level outcome")
			}
			constrained = true
		}
	}
	guardReasons := loadedGuardReasons(plan.Identity, outcome.LevelID, stateIdentity, states, gaps)
	if len(guardReasons) != 0 {
		switch outcome.Outcome {
		case LevelOutcomeNormal:
			if !loadedSeriesWarmingCompleted(outcome, plan, stateResults, states, gaps) {
				return resultContractViolation(codeOutcomeBusinessUnderActiveGuard, "active Runtime State or Plan gap guard forbids NORMAL")
			}
		case LevelOutcomeRecovery:
			// A guard forbids calling the Level normal. It does not forbid
			// closing what was opened, and it used to: a Level under a guard
			// could not recover until its window was FULL again, which for a
			// strategy whose window outlasts the interval between releases is
			// never (decision-022).
			//
			// Nothing is checked here in its place, deliberately. The evidence
			// for a recovery is counted where it is observed -- the trigger
			// walks the positions it actually saw -- and checked again on the
			// event contract, which refuses a RECOVERY whose own window
			// evidence does not carry it. A third reading here would have to
			// re-derive the same relation from the loaded state, and the one
			// it used to derive was "the mutation writes the Level FULL",
			// which is the very condition a hole in the window makes
			// unreachable. Removing it takes away a check that was reading the
			// wrong quantity, not a check of this.
		case LevelOutcomeUnknown:
			if !loadedSeriesWarmingCompleted(outcome, plan, stateResults, states, gaps) {
				// Either the reason the guard is already up for, or the
				// reason the marker this round ends with carries. Both are
				// exact -- two named values, not "any reason" -- and the
				// second has to be admitted because it is the guard that will
				// actually be covering this outcome.
				//
				// Read off the final marker through the same function the
				// exact-guard rule uses, not worked out again from the inputs.
				// Recomputing it here would put the fold in two places with
				// nothing comparing them, which is the shape this whole change
				// exists to remove.
				//
				// Only the first used to be, and a round that brought a new
				// incomplete input to an already guarded Level could then
				// satisfy neither rule: this one wanted the stored reason and
				// the exact-guard rule wanted the final marker's, which is the
				// new one. The Plan failed to evaluate on every Slot for as
				// long as the input stayed incomplete.
				if _, ok := guardReasons[outcome.ReasonCode]; !ok && disposition != PlanRetryPending &&
					!finalGapGuardsOutcome(finalMarkers, outcome) {
					return resultContractViolation(codeOutcomeUnknownDropsGuardReason, "UNKNOWN Level outcome does not preserve its active guard reason")
				}
				constrained = true
			}
		}
	}
	localized, err := validateLocalizedInputOutcome(input, plan.Identity, outcome)
	if err != nil {
		return err
	}
	constrained = constrained || localized
	effective, found := findEffectiveTimeFact(input, plan.Identity, outcome.LevelID, outcome.SeriesIdentityDigest)
	if !found {
		return resultContractViolation(codeOutcomeEffectiveTimeFactMissing, "Level outcome lacks its EffectiveTime fact")
	}
	if effective.Fact.Status() == strategy.EffectiveTimeUnknown && !constrained {
		if outcome.Outcome != LevelOutcomeUnknown || outcome.ReasonCode != ReasonCode(contract.ReasonEffectiveTimeUnknown) {
			return resultContractViolation(codeOutcomeEffectiveTimeUnknownMissed, "UNKNOWN EffectiveTime requires matching UNKNOWN Level outcome")
		}
	}
	affected := affectedBindings(input, plan.Identity, outcome.LevelID)
	partial := make(map[RequirementID]NamedInputBinding)
	for _, binding := range affected {
		switch binding.Completeness {
		case CompletenessUnavailable:
			if binding.Disposition == AccessTerminal {
				if outcome.Outcome != LevelOutcomeTerminal {
					return resultContractViolation(codeOutcomeTerminalDependencyMissed, "terminal dependency requires TERMINAL Level outcome")
				}
			} else if outcome.Outcome != LevelOutcomeUnknown && outcome.Outcome != LevelOutcomeTerminal {
				return resultContractViolation(codeOutcomeUnavailableDependencyBusine, "unavailable dependency cannot produce a business Level outcome")
			}
		case CompletenessPartial:
			partial[binding.RequirementID] = binding
		}
	}
	if len(partial) == 0 {
		if len(outcome.PartialProofs) != 0 {
			return resultContractViolation(codeProofOnFullOutcome, "FULL Level outcome must not carry PARTIAL proof receipts")
		}
		return nil
	}
	if outcome.Outcome != LevelOutcomeAbnormal {
		if outcome.Outcome == LevelOutcomeNormal || outcome.Outcome == LevelOutcomeRecovery {
			return resultContractViolation(codeOutcomePartialNotBusinessClear, "PARTIAL input cannot produce NORMAL or RECOVERY")
		}
		if len(outcome.PartialProofs) != 0 {
			return resultContractViolation(codeProofOnNonAbnormalPartial, "non-ABNORMAL PARTIAL outcome must not carry proof receipts")
		}
		return nil
	}
	capability, ok := partialCapability(plan, outcome.LevelID)
	if !ok || capability.Policy != PartialProvableAbnormalOnly || capability.Proof == nil {
		return resultContractViolation(codeProofCapabilityMissing, "PARTIAL ABNORMAL Level lacks a registered proof capability")
	}
	proofs := make(map[RequirementID]PartialDecisionProof, len(outcome.PartialProofs))
	for _, proof := range outcome.PartialProofs {
		if _, duplicate := proofs[proof.RequirementID]; duplicate {
			return resultContractViolation(codeProofDuplicate, "duplicate PARTIAL proof receipt")
		}
		binding, found := partial[proof.RequirementID]
		if !found || proof.DatasetName != binding.DatasetName || binding.PartialEvidence == nil ||
			proof.EvidenceDigest != binding.PartialEvidence.EvidenceDigest || proof.Result != PartialProofProvenAbnormal ||
			proof.Proof != *capability.Proof || !partialEvidenceSupports(capability, binding.PartialEvidence) {
			return resultContractViolation(codeProofDoesNotCloseEvidence, "PARTIAL proof receipt does not close its compiled evidence")
		}
		proofs[proof.RequirementID] = proof
	}
	if len(proofs) != len(partial) {
		return resultContractViolation(codeProofMissingForPartialInput, "every PARTIAL input affecting ABNORMAL requires one proof receipt")
	}
	return nil
}

func validateStateHistoryReplacements(
	plan DuePlan,
	stateResults []StateEvaluation,
	states StatePreflightResult,
) error {
	for _, state := range stateResults {
		loaded, found := states.Find(state.Mutation.Identity)
		if !found {
			return resultContractViolation(codeStateLoadedViewMissing, "State mutation lacks its loaded view")
		}
		if err := validateStateHistoryReplacement(loaded.History, state.Mutation, stateRetentionPoints(plan)); err != nil {
			return err
		}
	}
	return nil
}

func loadedSeriesWarmingCompleted(
	outcome LevelOutcome,
	plan DuePlan,
	stateResults []StateEvaluation,
	states StatePreflightResult,
	gaps GapLoadResult,
) bool {
	identity := StateKeyIdentity{
		Plan: plan.Identity, StateGeneration: plan.StateGeneration, SeriesIdentityDigest: outcome.SeriesIdentityDigest,
	}
	// A loaded WARMING or GAPPED Level converges when this record's mutation
	// writes it FULL without a reason: the live window formed the required full
	// Slots, exactly as the evaluator decides (guardConvergenceAllowed). A
	// series guard or a gap marker on the Level still forbids it.
	loaded, found := states.Find(identity)
	if !found || (loaded.Status != StateFoundWarming && loaded.Status != StateFoundGapped) || loaded.SeriesGuard != nil {
		return false
	}
	matchingLoadedLevel := false
	for _, level := range loaded.Levels {
		if level.LevelID != outcome.LevelID {
			continue
		}
		if matchingLoadedLevel || level.GapReasonCode == "" ||
			(level.HistoryCompleteness != HistoryWarming && level.HistoryCompleteness != HistoryGapped) {
			return false
		}
		matchingLoadedLevel = true
	}
	if !matchingLoadedLevel {
		return false
	}
	for _, marker := range gaps.Items {
		if marker.Status != GapFound || marker.Identity.Plan != plan.Identity {
			continue
		}
		for _, scope := range marker.Scopes {
			if !scope.Scope.HasLevel || scope.Scope.LevelID == outcome.LevelID {
				return false
			}
		}
	}
	matchingMutation := false
	for _, state := range stateResults {
		mutation := state.Mutation
		if mutation.Identity != identity {
			continue
		}
		if matchingMutation || mutation.SeriesGuard != nil || mutation.ValidateDigest() != nil ||
			mutation.ExpectedBlobRevision != loaded.BlobRevision {
			return false
		}
		matchingMutation = true
		matchingLevel := false
		for _, level := range mutation.Levels {
			if level.LevelID != outcome.LevelID {
				continue
			}
			if matchingLevel || level.HistoryCompleteness != HistoryFull || level.GapReasonCode != "" {
				return false
			}
			matchingLevel = true
		}
		if !matchingLevel {
			return false
		}
	}
	return matchingMutation
}

func loadedGuardReasons(
	plan PlanIdentity,
	levelID uint32,
	stateIdentity StateKeyIdentity,
	states StatePreflightResult,
	gaps GapLoadResult,
) map[ReasonCode]struct{} {
	reasons := make(map[ReasonCode]struct{})
	if view, found := states.Find(stateIdentity); found {
		if view.SeriesGuard != nil {
			reasons[view.SeriesGuard.ReasonCode] = struct{}{}
		}
		for _, level := range view.Levels {
			if level.LevelID == levelID &&
				(level.HistoryCompleteness == HistoryWarming || level.HistoryCompleteness == HistoryGapped) {
				reasons[level.GapReasonCode] = struct{}{}
			}
		}
	}
	for _, marker := range gaps.Items {
		if marker.Status != GapFound || marker.Identity.Plan != plan {
			continue
		}
		for _, scope := range marker.Scopes {
			if !scope.Scope.HasLevel || scope.Scope.LevelID == levelID {
				reasons[scope.ReasonCode] = struct{}{}
			}
		}
	}
	return reasons
}

func validateLocalizedInputOutcome(input InternalExecution, plan PlanIdentity, outcome LevelOutcome) (bool, error) {
	return localizedInputOutcome(input.Inputs, plan, outcome)
}

func localizedInputOutcome(bindings []NamedInputBinding, plan PlanIdentity, outcome LevelOutcome) (bool, error) {
	terminalReasons := make(map[ReasonCode]struct{})
	qualityReasons := make(map[ReasonCode]struct{})
	for _, binding := range affectedBindingsOf(bindings, plan, outcome.LevelID) {
		for _, terminal := range binding.Terminals {
			if inputFactMatchesOutcome(terminal.ImpactScope, terminal.RecordID, terminal.SourceTime, terminal.SeriesIdentity, outcome) {
				terminalReasons[terminal.ReasonCode] = struct{}{}
			}
		}
		for _, fact := range binding.QualityFacts {
			if inputFactMatchesOutcome(fact.ImpactScope, fact.RecordID, fact.SourceTime, fact.SeriesIdentity, outcome) {
				qualityReasons[fact.ReasonCode] = struct{}{}
			}
		}
	}
	if len(terminalReasons) != 0 {
		if outcome.Outcome != LevelOutcomeTerminal {
			return false, resultContractViolation(codeLocalizedTerminalNotTerminal, "localized terminal input requires TERMINAL Level outcome")
		}
		if _, ok := terminalReasons[outcome.ReasonCode]; !ok {
			return false, resultContractViolation(codeLocalizedTerminalReasonDiffers, "localized terminal reason differs from Level outcome")
		}
		return true, nil
	}
	if len(qualityReasons) != 0 {
		if outcome.Outcome != LevelOutcomeUnknown {
			return false, resultContractViolation(codeLocalizedQualityNotUnknown, "localized quality fact requires UNKNOWN Level outcome")
		}
		if _, ok := qualityReasons[outcome.ReasonCode]; !ok {
			return false, resultContractViolation(codeLocalizedQualityReasonDiffers, "localized quality reason differs from Level outcome")
		}
		return true, nil
	}
	return false, nil
}

func inputFactMatchesOutcome(
	impact ImpactScope,
	recordID string,
	sourceTime int64,
	series SeriesIdentityDigest,
	outcome LevelOutcome,
) bool {
	if impact != ImpactSeries {
		return true
	}
	return recordID == outcome.Record.RecordID && sourceTime == outcome.Record.SourceTime &&
		series == outcome.SeriesIdentityDigest
}

func affectedBindings(input InternalExecution, plan PlanIdentity, levelID uint32) []NamedInputBinding {
	return affectedBindingsOf(input.Inputs, plan, levelID)
}

func affectedBindingsOf(all []NamedInputBinding, plan PlanIdentity, levelID uint32) []NamedInputBinding {
	var bindings []NamedInputBinding
	for _, binding := range all {
		if binding.Consumer.Plan != plan || (binding.Consumer.HasLevel && binding.Consumer.LevelID != levelID) {
			continue
		}
		bindings = append(bindings, binding)
	}
	return bindings
}

func validateStateOutcomes(
	input InternalExecution,
	plan DuePlan,
	result PlanEvaluationResult,
	states StatePreflightResult,
	outcomes map[levelOutcomeIdentity]LevelOutcome,
) error {
	facts := make(map[levelOutcomeIdentity]LevelFactResult)
	for _, state := range result.StateResults {
		loaded, _ := states.Find(state.Mutation.Identity)
		anchors := make(map[RecordAnchor]struct{}, len(state.Mutation.AffectedRecords))
		for _, anchor := range state.Mutation.AffectedRecords {
			anchors[anchor] = struct{}{}
			foundBusiness := false
			foundGuardedNonBusiness := false
			for identity, outcome := range outcomes {
				if identity.Plan == state.Mutation.Identity.Plan &&
					identity.SeriesIdentityDigest == state.Mutation.Identity.SeriesIdentityDigest && identity.Record == anchor {
					switch outcome.Outcome {
					case LevelOutcomeNormal, LevelOutcomeAbnormal, LevelOutcomeRecovery:
						foundBusiness = true
					case LevelOutcomeUnknown, LevelOutcomeTerminal:
						if stateMutationGuardsOutcome(state.Mutation, outcome, true) ||
							stateUnknownMayAdvance(input, state.Mutation, outcome) {
							foundGuardedNonBusiness = true
						}
					}
				}
			}
			if !foundBusiness && !foundGuardedNonBusiness {
				return resultContractViolation(codeStateAnchorUnjustified, "State mutation anchor has neither a business outcome nor an exact durable guard")
			}
		}
		for _, point := range state.Mutation.Points {
			anchor := RecordAnchor{RecordID: point.RecordID, SourceTime: point.SourceTime}
			if _, ok := anchors[anchor]; !ok {
				continue
			}
			for _, fact := range point.Levels {
				outcome, ok := outcomes[levelOutcomeIdentity{
					Plan: state.Mutation.Identity.Plan, LevelID: fact.LevelID,
					SeriesIdentityDigest: state.Mutation.Identity.SeriesIdentityDigest, Record: anchor,
				}]
				if !ok {
					return resultContractViolation(codeStateFactOutcomeMissing, "State Level fact lacks a matching Level outcome")
				}
				effectiveStatus := ""
				if effective, found := findEffectiveTimeFact(input, outcome.Plan, outcome.LevelID, outcome.SeriesIdentityDigest); found {
					effectiveStatus = effective.Fact.Status()
				}
				if !levelFactMatchesOutcome(fact.Result, outcome.Outcome) &&
					!stateFactMayAdvanceUnknown(
						fact.Result, outcome, state.Mutation, effectiveStatus, stateInputAllowsAdvance(input, outcome),
					) &&
					!stateFactCarriedFromLoadedHistory(loaded.History, anchor, fact, outcome) {
					// Three predicates have to fail together to get here, and the
					// message named none of them: a deployment producing this
					// refusal said only that some fact disagreed with some
					// outcome, so telling "the fact is freshly written and really
					// disagrees" from "the fact was carried and the exemption did
					// not reach it" needed a reproduction nobody had. Each value
					// below is a closed vocabulary or a bool, so the text stays
					// bounded and carries no identity; who it happened to is on
					// the observation already.
					return resultContractViolation(codeStateFactContradictsOutcome,
						"State Level fact contradicts its Level outcome"+
							" (fact "+string(fact.Result)+
							", outcome "+string(outcome.Outcome)+
							", input full "+formatContractBool(stateInputAllowsAdvance(input, outcome))+
							", loaded point found "+formatContractBool(loadedHistoryHasAnchor(loaded.History, anchor))+
							", mutation guards outcome "+formatContractBool(stateMutationGuardsOutcome(state.Mutation, outcome, true))+")")
				}
				identity := levelOutcomeIdentity{
					Plan: state.Mutation.Identity.Plan, LevelID: fact.LevelID,
					SeriesIdentityDigest: state.Mutation.Identity.SeriesIdentityDigest, Record: anchor,
				}
				if _, duplicate := facts[identity]; duplicate {
					return resultContractViolation(codeStateFactDuplicate, "duplicate State fact for one Level outcome")
				}
				facts[identity] = fact.Result
			}
		}
		for _, level := range state.Mutation.Levels {
			for identity, outcome := range outcomes {
				if identity.Plan != state.Mutation.Identity.Plan || identity.LevelID != level.LevelID ||
					identity.SeriesIdentityDigest != state.Mutation.Identity.SeriesIdentityDigest || len(outcome.PartialProofs) == 0 {
					continue
				}
				if level.HistoryCompleteness != HistoryWarming && level.HistoryCompleteness != HistoryGapped {
					return resultContractViolation(codeStatePartialAbnormalDropsProven, "PARTIAL ABNORMAL State must preserve recovery warming/gap provenance")
				}
			}
		}
	}
	for identity, outcome := range outcomes {
		if outcome.Outcome != LevelOutcomeNormal && outcome.Outcome != LevelOutcomeAbnormal && outcome.Outcome != LevelOutcomeRecovery {
			continue
		}
		fact, ok := facts[identity]
		if !ok || !levelFactMatchesOutcome(fact, outcome.Outcome) {
			return resultContractViolation(codeStateFactMissingForOutcome, "successful Level outcome lacks its State fact")
		}
	}
	return nil
}

func stateRetentionPoints(plan DuePlan) uint32 {
	var retention uint32
	if plan.CompiledPlan == nil {
		return 0
	}
	for _, level := range plan.CompiledPlan.Levels() {
		if points := level.StateRequirement().RetentionPoints; points > retention {
			retention = points
		}
	}
	return retention
}

// validateStateHistoryReplacement proves that Points is this round's addition
// to the loaded record and nothing else: every point anchored in this batch,
// ordered and unique, and where it lands on a position the loaded history
// already holds, the same record carrying at least the facts that were stored.
// BaseHistory must be the history that was loaded, and the retention bound the
// one the compiled Plan asks for.
//
// It used to prove that Points was the whole bounded window. The window is
// still what gets written; it is now assembled at serialization from these two
// fields, so what has to be proved here is that the pair describes it.
func validateStateHistoryReplacement(loaded []StateHistoryPoint, mutation StateMutation, retention uint32) error {
	if retention == 0 {
		return resultContractViolation(codeHistoryRetentionBoundMissing, "State history replacement lacks a retention bound")
	}
	// Derived by the producer, derived again here from the compiled Plan, and
	// compared. One derivation would let the bound the write uses drift from
	// the bound the Plan asks for with nothing to notice.
	if mutation.RetentionPoints != retention {
		return resultContractViolation(codeHistoryRetentionBoundMissing, "State mutation carries a retention bound the Plan does not ask for")
	}
	if !isLoadedHistoryItself(mutation.BaseHistory, loaded) {
		return resultContractViolation(codeHistoryLoadedPointChanged, "State mutation base is not the loaded history")
	}
	// Both lookups are over the addition, which is one point in an ordinary
	// round, so neither indexes the loaded record: a map keyed by every stored
	// point would cost per retained point, in time and in memory, which is the
	// per-round window cost this shape removed. The addition's anchors are
	// found by scanning the anchors this batch names, and the loaded position a
	// point lands on by binary search over a record the loader keeps ordered.
	for index, point := range mutation.Points {
		if index > 0 && StateHistoryOrder(mutation.Points[index-1], point) >= 0 {
			return resultContractViolation(codeHistorySnapshotIncomplete, "State history addition is not ordered and unique")
		}
		anchor := RecordAnchor{RecordID: point.RecordID, SourceTime: point.SourceTime}
		current := false
		for _, candidate := range mutation.AffectedRecords {
			if candidate == anchor {
				current = true
				break
			}
		}
		if !current {
			return resultContractViolation(codeHistoryLoadedPointInvented, "State history addition carries a point this batch did not evaluate")
		}
		position := sort.Search(len(loaded), func(index int) bool { return loaded[index].SourceTime >= point.SourceTime })
		if position == len(loaded) || loaded[position].SourceTime != point.SourceTime ||
			loaded[position].RecordID != point.RecordID {
			continue
		}
		stored := loaded[position]
		// The addition replaces the stored point at that position, so it has to
		// carry what was stored there. A fact that disappears this way is a
		// silent history rewrite: the point stays, the Level's past does not.
		for _, fact := range stored.Levels {
			if !containsStateLevelFact(point.Levels, fact) {
				return resultContractViolation(codeHistoryLoadedPointChanged, "State history addition drops a stored Level fact")
			}
		}
	}
	return nil
}

// isLoadedHistoryItself reports whether base is the loaded history, not a copy
// of it: the same length and the same backing array.
//
// Identity rather than equality, for two reasons. The property the mutation
// claims is that it references the record the Slot loaded - the whole point of
// carrying a base instead of a rebuilt window - and a copy with equal content
// is precisely the thing this shape exists to remove, so accepting one would
// leave the cost in place and the check reporting success. And comparing the
// content costs what the copy cost: over a 1469 point window a per-point deep
// comparison measured 310 us, 155 KB and 3085 allocations for one series in one
// round, which is the window allocation again under another name, moved from
// the producer into the contract check where the benchmark on the producer
// cannot see it.
func isLoadedHistoryItself(base, loaded []StateHistoryPoint) bool {
	if len(base) != len(loaded) {
		return false
	}
	if len(loaded) == 0 {
		return true
	}
	return &base[0] == &loaded[0]
}

func containsStateLevelFact(facts []StateLevelFact, fact StateLevelFact) bool {
	for _, candidate := range facts {
		if candidate == fact {
			return true
		}
	}
	return false
}

func stateUnknownMayAdvance(input InternalExecution, mutation StateMutation, outcome LevelOutcome) bool {
	if !stateInputAllowsAdvance(input, outcome) {
		return false
	}
	if stateMutationGuardsOutcome(mutation, outcome, true) {
		return true
	}
	effective, found := findEffectiveTimeFact(input, outcome.Plan, outcome.LevelID, outcome.SeriesIdentityDigest)
	return found && effective.Fact.Status() == strategy.EffectiveTimeInactive &&
		outcome.ReasonCode == ReasonCode(contract.ReasonEffectiveTimeInactive)
}

func stateFactMayAdvanceUnknown(
	fact LevelFactResult,
	outcome LevelOutcome,
	mutation StateMutation,
	effectiveStatus string,
	inputIsFull bool,
) bool {
	if !inputIsFull || outcome.Outcome != LevelOutcomeUnknown ||
		(fact != LevelFactNormal && fact != LevelFactAnomalous) {
		return false
	}
	if stateMutationGuardsOutcome(mutation, outcome, true) {
		return true
	}
	return effectiveStatus == strategy.EffectiveTimeInactive &&
		outcome.ReasonCode == ReasonCode(contract.ReasonEffectiveTimeInactive)
}

// stateFactCarriedFromLoadedHistory reports whether the fact is one the
// history already held at this anchor before the round, and this round's
// outcome for its Level is not a business outcome.
//
// The point a round writes for a record it evaluates again is the stored
// point with the round's fresh facts merged in, and a fresh fact is only
// written for a Level the round advanced or guarded. A Level the round did
// neither for keeps the stored fact, so the point carries a statement an
// earlier round made and checked against its own outcome. This round's
// outcome for that Level can be UNKNOWN or TERMINAL under thinner input
// without either statement being wrong, and judging the carried fact by it
// rejected every re-evaluation of such a record. A business outcome is never
// exempt: the round advanced that Level, wrote a fresh fact for it, and the
// merge already required the two to agree, so the fact is the round's own.
func stateFactCarriedFromLoadedHistory(
	history []StateHistoryPoint,
	anchor RecordAnchor,
	fact StateLevelFact,
	outcome LevelOutcome,
) bool {
	if outcome.Outcome != LevelOutcomeUnknown && outcome.Outcome != LevelOutcomeTerminal {
		return false
	}
	for _, point := range history {
		if point.RecordID != anchor.RecordID || point.SourceTime != anchor.SourceTime {
			continue
		}
		for _, held := range point.Levels {
			if held == fact {
				return true
			}
		}
		return false
	}
	return false
}

func stateInputAllowsAdvance(input InternalExecution, outcome LevelOutcome) bool {
	return InputAllowsStateAdvance(input.Inputs, outcome)
}

// InputAllowsStateAdvance says whether the named inputs bound to the
// outcome's Level were complete enough for a fact detected on them to enter
// the Level's history while the outcome itself is UNKNOWN: every binding of
// the Level is FULL, carries data, is available, and localizes nothing to
// this record. The contract judges a State fact under an UNKNOWN outcome by
// it, and the evaluator asks it before it lets such a fact advance, so the
// two never disagree about the same round.
func InputAllowsStateAdvance(all []NamedInputBinding, outcome LevelOutcome) bool {
	bindings := affectedBindingsOf(all, outcome.Plan, outcome.LevelID)
	if len(bindings) == 0 {
		return false
	}
	for _, binding := range bindings {
		if binding.Completeness != CompletenessFull || binding.DataState != DataStateData ||
			binding.Disposition != AccessAvailable {
			return false
		}
	}
	localized, err := localizedInputOutcome(all, outcome.Plan, outcome)
	return err == nil && !localized
}

func validateDegradedGuardCoverage(
	input InternalExecution,
	result PlanEvaluationResult,
	states StatePreflightResult,
	gaps GapLoadResult,
) error {
	loadedMarkers := loadedGapScopes(gaps, result.Plan)
	preEventMarkers := cloneGapScopes(loadedMarkers)
	applyGapMutations(preEventMarkers, result.GuardBeforeEvents)
	finalMarkers := cloneGapScopes(preEventMarkers)
	applyGapMutations(finalMarkers, result.GuardAfterState)
	finalStates := finalStateViews(result, states)

	obligations := 0
	for _, outcome := range result.LevelOutcomes {
		requiresGuard := outcome.Outcome == LevelOutcomeUnknown || outcome.Outcome == LevelOutcomeTerminal || len(outcome.PartialProofs) != 0
		if !requiresGuard {
			continue
		}
		if outcome.Outcome == LevelOutcomeUnknown && stateInputAllowsAdvance(input, outcome) {
			effective, found := findEffectiveTimeFact(input, outcome.Plan, outcome.LevelID, outcome.SeriesIdentityDigest)
			if found && effective.Fact.Status() == strategy.EffectiveTimeInactive &&
				outcome.ReasonCode == ReasonCode(contract.ReasonEffectiveTimeInactive) {
				continue
			}
		}
		obligations++
		if result.Disposition == PlanRetryPending && outcome.Outcome == LevelOutcomeUnknown {
			continue
		}
		if len(outcome.PartialProofs) != 0 {
			for _, proof := range outcome.PartialProofs {
				binding, found := partialBinding(input, outcome, proof.RequirementID)
				if !found || !preEventMarkerCovers(preEventMarkers, loadedMarkers, binding, outcome) ||
					!finalMarkerCovers(finalMarkers, binding, outcome) {
					return resultContractViolation(codeGuardMissingForPartialAbnormal, "PARTIAL ABNORMAL lacks its final Plan/Level gap guard")
				}
			}
			continue
		}
		if loadedStateGuardsTerminalOutcome(states, outcome) || loadedGapGuardsTerminalOutcome(gaps, outcome) {
			// The blob this round read is itself the guard. A series whose
			// stored state cannot be decoded produces a TERMINAL outcome
			// naming that, and there is no marker to point at: the record is
			// the persistent fact, read again identically on every round, and
			// more durable than anything a writer could put beside it.
			// Without this the contract refused the Level every round for as
			// long as the bad record sat there, and the refusal named nothing
			// anybody could clear.
			continue
		}
		if finalStateGuardsOutcome(finalStates, outcome) || finalGapGuardsOutcome(finalMarkers, outcome) {
			continue
		}
		return resultContractViolation(codeGuardMissingForDegradedOutcome,
			"degraded Level outcome lacks an exact durable guard"+
				describeMissingGuard(input, result, loadedMarkers, finalMarkers, finalStates, outcome))
	}
	if obligations == 0 &&
		(result.Disposition == PlanUnavailable || result.Disposition == PlanReadinessGap || result.Disposition == PlanTerminal) &&
		!hasFinalPlanGap(finalMarkers) {
		return resultContractViolation(codeGuardMissingPreEvent, "degraded plan completion requires a durable pre-event gap guard")
	}
	return nil
}

// describeMissingGuard renders what the two comparisons saw when neither
// guarded a degraded outcome. The refusal used to say only that it refused,
// and a residue of it on two objects could not be attributed: whether the
// outcome carried this round's fold, the stored marker's reason or a local
// one, and whether the State guard or the marker was the one that did not
// match, were not in the line. Each value is a closed vocabulary, a bounded
// integer or yes/no; who it happened to is on the observation already.
func describeMissingGuard(
	input InternalExecution, result PlanEvaluationResult,
	loadedMarkers, finalMarkers map[GapScope]GapScopeState,
	finalStates map[StateKeyIdentity]RuntimeStateView, outcome LevelOutcome,
) string {
	seriesGuard, levelGuard, stateWritten := "none", "none", false
	for identity, state := range finalStates {
		if identity.Plan != outcome.Plan || identity.SeriesIdentityDigest != outcome.SeriesIdentityDigest {
			continue
		}
		if state.SeriesGuard != nil {
			seriesGuard = string(state.SeriesGuard.ReasonCode)
		}
		for _, level := range state.Levels {
			if level.LevelID == outcome.LevelID {
				levelGuard = string(level.HistoryCompleteness) + "/" + contractReasonOrNone(level.GapReasonCode)
			}
		}
	}
	for _, state := range result.StateResults {
		if state.Mutation.Identity.Plan == outcome.Plan && state.Mutation.Identity.SeriesIdentityDigest == outcome.SeriesIdentityDigest {
			stateWritten = true
		}
	}
	// Empty when this round proposes no guard for the Level.
	fold, _ := RoundGuardReasonForLevel(input.Inputs, outcome.Plan, outcome.LevelID)
	outcomesForLevel := 0
	for _, other := range result.LevelOutcomes {
		if other.Plan == outcome.Plan && other.LevelID == outcome.LevelID && other.SeriesIdentityDigest == outcome.SeriesIdentityDigest {
			outcomesForLevel++
		}
	}
	return " (outcome " + string(outcome.Outcome) +
		", reason " + contractReasonOrNone(outcome.ReasonCode) +
		", level " + strconv.FormatUint(uint64(outcome.LevelID), 10) +
		", outcomes for level " + strconv.Itoa(outcomesForLevel) +
		", input full " + formatContractBool(stateInputAllowsAdvance(input, outcome)) +
		", inputs " + describeAffectedInputs(input.Inputs, outcome) +
		", round fold " + contractReasonOrNone(fold) +
		", state series guard " + seriesGuard +
		", state level guard " + levelGuard +
		", state written " + formatContractBool(stateWritten) +
		", marker plan loaded " + markerReasonOrNone(loadedMarkers, GapScope{}) +
		", marker level loaded " + markerReasonOrNone(loadedMarkers, GapScope{HasLevel: true, LevelID: outcome.LevelID}) +
		", marker plan final " + markerReasonOrNone(finalMarkers, GapScope{}) +
		", marker level final " + markerReasonOrNone(finalMarkers, GapScope{HasLevel: true, LevelID: outcome.LevelID}) +
		", guard proposed " + formatContractBool(len(result.GuardBeforeEvents) != 0 || len(result.GuardAfterState) != 0) + ")"
}

// maxDescribedInputs bounds how many of the Level's bindings the refusal
// spells out; the rest are counted. A Level rarely has more than a handful.
const maxDescribedInputs = 6

// describeAffectedInputs renders the bindings "input full" was decided over,
// one term per binding, as scope:role:completeness/data/disposition, and
// whether a quality or terminal fact localized the outcome to this record.
// "input full no" alone could not say which of its four conjuncts failed;
// the round fold reads only completeness, so a binding that is FULL yet
// EMPTY, or degraded without being partial, is invisible to it and visible
// here. Every value is a closed vocabulary.
func describeAffectedInputs(all []NamedInputBinding, outcome LevelOutcome) string {
	bindings := affectedBindingsOf(all, outcome.Plan, outcome.LevelID)
	if len(bindings) == 0 {
		return "none"
	}
	terms := make([]string, 0, len(bindings)+1)
	for index, binding := range bindings {
		if index == maxDescribedInputs {
			terms = append(terms, "+"+strconv.Itoa(len(bindings)-index))
			break
		}
		scope := "plan"
		if binding.Consumer.HasLevel {
			scope = "level"
		}
		terms = append(terms, scope+":"+string(binding.Role)+":"+string(binding.Completeness)+"/"+
			inputDataStateOrUnknown(binding.DataState)+"/"+string(binding.Disposition))
	}
	localized, err := localizedInputOutcome(all, outcome.Plan, outcome)
	localizedText := formatContractBool(err == nil && localized)
	if err != nil {
		localizedText = "error"
	}
	return "[" + strings.Join(terms, " ") + "] localized " + localizedText
}

func inputDataStateOrUnknown(state DataState) string {
	if state == DataStateUnknown {
		return "UNKNOWN"
	}
	return string(state)
}

func contractReasonOrNone(reason ReasonCode) string {
	if reason == "" {
		return "none"
	}
	return string(reason)
}

func markerReasonOrNone(markers map[GapScope]GapScopeState, scope GapScope) string {
	marker, found := markers[scope]
	if !found {
		return "none"
	}
	return string(marker.Status) + "/" + contractReasonOrNone(marker.ReasonCode)
}

func loadedGapScopes(gaps GapLoadResult, plan PlanIdentity) map[GapScope]GapScopeState {
	scopes := make(map[GapScope]GapScopeState)
	for _, marker := range gaps.Items {
		if marker.Status != GapFound || marker.Identity.Plan != plan {
			continue
		}
		for _, scope := range marker.Scopes {
			scopes[scope.Scope] = scope
		}
	}
	return scopes
}

func cloneGapScopes(scopes map[GapScope]GapScopeState) map[GapScope]GapScopeState {
	cloned := make(map[GapScope]GapScopeState, len(scopes))
	for scope, state := range scopes {
		cloned[scope] = state
	}
	return cloned
}

func applyGapMutations(scopes map[GapScope]GapScopeState, mutations []PlanGapMutation) {
	for _, mutation := range mutations {
		for _, update := range mutation.Scopes {
			switch update.Kind {
			case GapClear:
				delete(scopes, update.Scope)
			case GapOpen, GapStrengthen:
				scopes[update.Scope] = GapScopeState{
					Scope: update.Scope, Status: GapStatusGapped, ReasonCode: update.ReasonCode,
					RequiredFullSlots: update.RequiredFullSlots,
				}
			case GapWarmup:
				scopes[update.Scope] = GapScopeState{
					Scope: update.Scope, Status: GapStatusWarming, ReasonCode: update.ReasonCode,
					RequiredFullSlots: update.RequiredFullSlots,
				}
			}
		}
	}
}

func finalStateViews(result PlanEvaluationResult, states StatePreflightResult) map[StateKeyIdentity]RuntimeStateView {
	final := make(map[StateKeyIdentity]RuntimeStateView, len(states.Items))
	for _, view := range states.Items {
		final[view.Identity] = view
	}
	for _, state := range result.StateResults {
		mutation := state.Mutation
		final[mutation.Identity] = RuntimeStateView{
			Identity: mutation.Identity, SeriesGuard: mutation.SeriesGuard,
			Levels: runtimeLevelsFromMutation(mutation.Levels),
		}
	}
	return final
}

func runtimeLevelsFromMutation(levels []RuntimeLevelStateMutation) []RuntimeLevelStateView {
	result := make([]RuntimeLevelStateView, len(levels))
	for index, level := range levels {
		result[index] = RuntimeLevelStateView{
			LevelID: level.LevelID, HistoryCompleteness: level.HistoryCompleteness,
			GapReasonCode: level.GapReasonCode, WarmupRequirementRef: level.WarmupRequirementRef,
			LevelStateCompatibility: level.LevelStateCompatibility,
		}
	}
	return result
}

func partialBinding(input InternalExecution, outcome LevelOutcome, requirement RequirementID) (NamedInputBinding, bool) {
	for _, binding := range affectedBindings(input, outcome.Plan, outcome.LevelID) {
		if binding.RequirementID == requirement && binding.Completeness == CompletenessPartial {
			return binding, true
		}
	}
	return NamedInputBinding{}, false
}

func requiredGapScope(binding NamedInputBinding, outcome LevelOutcome) GapScope {
	if binding.Consumer.HasLevel {
		return GapScope{HasLevel: true, LevelID: outcome.LevelID}
	}
	return GapScope{}
}

func preEventMarkerCovers(
	markers map[GapScope]GapScopeState,
	loaded map[GapScope]GapScopeState,
	binding NamedInputBinding,
	outcome LevelOutcome,
) bool {
	required := requiredGapScope(binding, outcome)
	if markerReasonMatches(markers, required, binding.ReasonCode) {
		return true
	}
	// An already-active Plan guard protected the Level before this Slot. A new
	// Plan-wide mutation must not widen a Level-only PARTIAL obligation.
	return required.HasLevel && markerReasonMatches(loaded, GapScope{}, binding.ReasonCode)
}

func finalMarkerCovers(markers map[GapScope]GapScopeState, binding NamedInputBinding, outcome LevelOutcome) bool {
	required := requiredGapScope(binding, outcome)
	if markerReasonMatches(markers, required, binding.ReasonCode) {
		return true
	}
	return required.HasLevel && markerReasonMatches(markers, GapScope{}, binding.ReasonCode)
}

func markerReasonMatches(markers map[GapScope]GapScopeState, scope GapScope, reason ReasonCode) bool {
	marker, found := markers[scope]
	return found && marker.ReasonCode == reason
}

func finalStateGuardsOutcome(states map[StateKeyIdentity]RuntimeStateView, outcome LevelOutcome) bool {
	for identity, state := range states {
		if identity.Plan != outcome.Plan || identity.SeriesIdentityDigest != outcome.SeriesIdentityDigest {
			continue
		}
		if state.SeriesGuard != nil && state.SeriesGuard.ReasonCode == outcome.ReasonCode {
			return true
		}
		for _, level := range state.Levels {
			if level.LevelID == outcome.LevelID &&
				(level.HistoryCompleteness == HistoryWarming || level.HistoryCompleteness == HistoryGapped) &&
				level.GapReasonCode == outcome.ReasonCode {
				return true
			}
		}
	}
	return false
}

// loadedGapGuardsTerminalOutcome reports whether a TERMINAL outcome is covered
// by the gap marker that caused it. A marker that loaded terminal is the
// persistent fact behind every Level of the Plan being terminal, and there is
// no marker to point at because the marker is the thing that is broken.
//
// Three conditions, and each is load-bearing -- see
// loadedStateGuardsTerminalOutcome, which is the same rule for the other
// record.
func loadedGapGuardsTerminalOutcome(gaps GapLoadResult, outcome LevelOutcome) bool {
	if outcome.Outcome != LevelOutcomeTerminal {
		return false
	}
	for _, marker := range gaps.Items {
		if marker.Identity.Plan != outcome.Plan {
			continue
		}
		if marker.Status == GapTerminal && marker.ReasonCode == outcome.ReasonCode {
			return true
		}
	}
	return false
}

// loadedStateGuardsTerminalOutcome reports whether a TERMINAL outcome is
// covered by the loaded record that caused it.
//
// Only TERMINAL, and only when the loaded view of that series says the record
// could not be read and says it with the outcome's own reason. All three
// matter. Widening it to any outcome would let a business UNKNOWN pass with no
// guard at all; dropping the reason comparison would let a Level terminal for
// one cause be covered by a record broken for another.
//
// The outcome-kind half cannot be reached through Validate today -- a
// non-TERMINAL outcome beside a DeterministicInvalid series is already refused
// by codeOutcomeInvalidSeriesNotTerminal above -- so all three are pinned at
// the predicate in result_contract_internal_test.go. That rule is a separate
// rule, and this line is what holds if it is ever relaxed.
//
// It reads the loaded views rather than the final ones on purpose: a series
// whose record could not be decoded produces no mutation, so the two are the
// same here -- and the loaded set is where the fact is, which is what this
// rule is about.
func loadedStateGuardsTerminalOutcome(states StatePreflightResult, outcome LevelOutcome) bool {
	if outcome.Outcome != LevelOutcomeTerminal {
		return false
	}
	for _, view := range states.Items {
		if view.Identity.Plan != outcome.Plan || view.Identity.SeriesIdentityDigest != outcome.SeriesIdentityDigest {
			continue
		}
		if view.Status == StateDeterministicInvalid && view.ReasonCode == outcome.ReasonCode {
			return true
		}
	}
	return false
}

func finalGapGuardsOutcome(markers map[GapScope]GapScopeState, outcome LevelOutcome) bool {
	return markerReasonMatches(markers, GapScope{}, outcome.ReasonCode) ||
		markerReasonMatches(markers, GapScope{HasLevel: true, LevelID: outcome.LevelID}, outcome.ReasonCode)
}

func hasFinalPlanGap(markers map[GapScope]GapScopeState) bool {
	_, found := markers[GapScope{}]
	return found
}

func stateMutationGuardsOutcome(mutation StateMutation, outcome LevelOutcome, requireSameReason bool) bool {
	if mutation.Identity.Plan != outcome.Plan || mutation.Identity.SeriesIdentityDigest != outcome.SeriesIdentityDigest {
		return false
	}
	if mutation.SeriesGuard != nil && (!requireSameReason || mutation.SeriesGuard.ReasonCode == outcome.ReasonCode) {
		return true
	}
	for _, level := range mutation.Levels {
		if level.LevelID == outcome.LevelID &&
			(level.HistoryCompleteness == HistoryWarming || level.HistoryCompleteness == HistoryGapped) &&
			(!requireSameReason || level.GapReasonCode == outcome.ReasonCode) {
			return true
		}
	}
	return false
}

func stateMutationHasLevelGuard(mutation StateMutation) bool {
	for _, level := range mutation.Levels {
		if level.HistoryCompleteness == HistoryWarming || level.HistoryCompleteness == HistoryGapped {
			return true
		}
	}
	return false
}

func levelFactMatchesOutcome(fact LevelFactResult, outcome LevelOutcomeKind) bool {
	switch fact {
	case LevelFactNormal:
		return outcome == LevelOutcomeNormal || outcome == LevelOutcomeRecovery
	case LevelFactAnomalous:
		return outcome == LevelOutcomeNormal || outcome == LevelOutcomeAbnormal || outcome == LevelOutcomeRecovery
	case LevelFactUnavailable:
		return outcome == LevelOutcomeUnknown
	case LevelFactError:
		return outcome == LevelOutcomeTerminal
	default:
		return false
	}
}

func validateEventOutcomes(input InternalExecution, result PlanEvaluationResult, outcomes map[levelOutcomeIdentity]LevelOutcome) error {
	type recordIdentity struct {
		Series SeriesIdentityDigest
		Record RecordAnchor
	}
	expectedEvents := make(map[recordIdentity]string)
	// A record whose RECOVERY envelope the trigger held expects no event.
	// The hold is a statement about the record, so it has to be the same on
	// every RECOVERY outcome of that record, and it cannot coexist with an
	// ABNORMAL outcome there: an abnormal Level decides the record before
	// the gate is consulted, so a hold beside it is a contradiction.
	held := make(map[recordIdentity]bool)
	for identity, outcome := range outcomes {
		effective, found := findEffectiveTimeFact(input, identity.Plan, identity.LevelID, identity.SeriesIdentityDigest)
		if !found {
			return resultContractViolation(codeEventEffectiveTimeFactMissing, "TriggerEvent validation lacks EffectiveTime fact")
		}
		if effective.Fact.Status() != strategy.EffectiveTimeActive {
			continue
		}
		record := recordIdentity{Series: identity.SeriesIdentityDigest, Record: identity.Record}
		if outcome.EnvelopeHeld && outcome.Outcome != LevelOutcomeRecovery {
			return resultContractViolation(codeEventHeldOnNonRecoveryOutcome, "a held envelope is a property of RECOVERY outcomes only")
		}
		switch outcome.Outcome {
		case LevelOutcomeAbnormal:
			expectedEvents[record] = contract.TriggerEventAbnormal
		case LevelOutcomeRecovery:
			if previous, seen := held[record]; seen && previous != outcome.EnvelopeHeld {
				return resultContractViolation(codeEventHeldDisagreesAcrossLevels, "RECOVERY outcomes of one record disagree on whether its envelope was held")
			}
			held[record] = outcome.EnvelopeHeld
			if expectedEvents[record] == "" {
				expectedEvents[record] = contract.TriggerEventRecovery
			}
		}
	}
	for record, wasHeld := range held {
		if !wasHeld {
			continue
		}
		if expectedEvents[record] == contract.TriggerEventAbnormal {
			return resultContractViolation(codeEventHeldOnNonRecoveryOutcome, "a held envelope cannot stand beside an ABNORMAL outcome of the same record")
		}
		delete(expectedEvents, record)
	}
	actualEvents := make(map[recordIdentity]struct{})
	for _, state := range result.StateResults {
		for _, event := range state.Events {
			record := recordIdentity{
				Series: state.Mutation.Identity.SeriesIdentityDigest,
				Record: RecordAnchor{RecordID: event.RecordRef.RecordID, SourceTime: event.RecordRef.SourceTime},
			}
			if _, duplicate := actualEvents[record]; duplicate {
				return resultContractViolation(codeEventDuplicate, "duplicate TriggerEvent for one Plan series record")
			}
			if expectedEvents[record] == "" || event.EventKind != expectedEvents[record] {
				return resultContractViolation(codeEventKindMismatch, "TriggerEvent kind does not match Level outcomes")
			}
			actualEvents[record] = struct{}{}
			seen := make(map[uint32]struct{}, len(event.LevelResults))
			for _, level := range event.LevelResults {
				outcome, ok := outcomes[levelOutcomeIdentity{
					Plan: result.Plan, LevelID: level.LevelID, SeriesIdentityDigest: state.Mutation.Identity.SeriesIdentityDigest,
					Record: RecordAnchor{RecordID: event.RecordRef.RecordID, SourceTime: event.RecordRef.SourceTime},
				}]
				effective, effectiveFound := findEffectiveTimeFact(
					input, result.Plan, level.LevelID, state.Mutation.Identity.SeriesIdentityDigest,
				)
				if !ok || !effectiveFound || string(outcome.Outcome) != level.Result ||
					level.DetectEvidence.EffectiveTimeStatus != string(effective.Fact.Status()) {
					return resultContractViolation(codeEventLevelResultContradicts, "TriggerEvent Level result contradicts Level outcome decomposition")
				}
				seen[level.LevelID] = struct{}{}
			}
			for identity, outcome := range outcomes {
				effective, _ := findEffectiveTimeFact(input, identity.Plan, identity.LevelID, identity.SeriesIdentityDigest)
				if identity.Plan == result.Plan && identity.SeriesIdentityDigest == state.Mutation.Identity.SeriesIdentityDigest &&
					identity.Record.RecordID == event.RecordRef.RecordID && identity.Record.SourceTime == event.RecordRef.SourceTime &&
					effective.Fact.Status() == strategy.EffectiveTimeActive &&
					(outcome.Outcome == LevelOutcomeNormal || outcome.Outcome == LevelOutcomeAbnormal || outcome.Outcome == LevelOutcomeRecovery) {
					if _, ok := seen[identity.LevelID]; !ok {
						return resultContractViolation(codeEventOmitsSiblingOutcome, "TriggerEvent omitted a successful sibling Level outcome")
					}
				}
			}
		}
	}
	// An event decided and not kept, because its protocol has no message for
	// its kind, stands for the record's envelope as an event would: the same
	// record identity, the same kind, never both. It carries no content to
	// check - that is what not keeping it means - so what is checked is that
	// the protocol really has no message for it: a kept identity in place of
	// an event the consumer would have received is a lost event.
	for _, state := range result.StateResults {
		for _, dropped := range state.WithoutMessage {
			if contract.EventHasMessage(dropped.Format, dropped.EventKind) {
				return resultContractViolation(codeEventKindMismatch, "an event its protocol has a message for was not kept")
			}
			record := recordIdentity{Series: state.Mutation.Identity.SeriesIdentityDigest, Record: dropped.Record}
			if _, duplicate := actualEvents[record]; duplicate {
				return resultContractViolation(codeEventDuplicate, "duplicate TriggerEvent for one Plan series record")
			}
			if expectedEvents[record] == "" || dropped.EventKind != expectedEvents[record] {
				return resultContractViolation(codeEventKindMismatch, "TriggerEvent kind does not match Level outcomes")
			}
			actualEvents[record] = struct{}{}
		}
	}
	if len(actualEvents) != len(expectedEvents) {
		return resultContractViolation(codeEventEnvelopeCountInvalid, "ABNORMAL or RECOVERY Level outcomes require exactly one TriggerEvent envelope")
	}
	for record := range expectedEvents {
		if _, found := actualEvents[record]; !found {
			return resultContractViolation(codeEventMissingForBusinessOutcome, "missing TriggerEvent for business Level outcome")
		}
	}
	return nil
}
