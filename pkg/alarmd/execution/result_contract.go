// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"errors"
	"reflect"
	"sort"

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
					return nil, errors.New("alarmd execution: selected PRIMARY record is missing")
				}
				add(SeriesIdentityDigest(record.DimensionIdentity().Digest), RecordAnchor{
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
	seen := make(map[levelOutcomeIdentity]LevelOutcome, len(result.LevelOutcomes))
	for _, outcome := range result.LevelOutcomes {
		identity := levelOutcomeIdentity{
			Plan: outcome.Plan, LevelID: outcome.LevelID, SeriesIdentityDigest: outcome.SeriesIdentityDigest, Record: outcome.Record,
		}
		if _, ok := wanted[identity]; !ok {
			return errors.New("alarmd execution: Level outcome does not belong to a selected Plan record Level")
		}
		if _, duplicate := seen[identity]; duplicate {
			return errors.New("alarmd execution: duplicate Level outcome")
		}
		if err := validateLevelOutcome(input, plan, result.Disposition, outcome, result.StateResults, states, gaps); err != nil {
			return err
		}
		seen[identity] = outcome
	}
	if len(seen) != len(wanted) {
		return errors.New("alarmd execution: every selected Plan record Level requires one outcome")
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
) error {
	if outcome.Plan != plan.Identity || !compiledPlanHasLevel(plan.CompiledPlan, outcome.LevelID) ||
		outcome.SeriesIdentityDigest == "" {
		return errors.New("alarmd execution: incomplete Level outcome identity")
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
		return errors.New("alarmd execution: invalid Level outcome")
	}
	stateIdentity := StateKeyIdentity{Plan: plan.Identity, StateGeneration: plan.StateGeneration, SeriesIdentityDigest: outcome.SeriesIdentityDigest}
	constrained := false
	if view, found := states.Find(stateIdentity); found {
		switch view.Status {
		case StateRetryableIO:
			if outcome.Outcome != LevelOutcomeUnknown || outcome.ReasonCode != view.ReasonCode {
				return errors.New("alarmd execution: retryable State series requires matching UNKNOWN Level outcome")
			}
			constrained = true
		case StateDeterministicInvalid:
			if outcome.Outcome != LevelOutcomeTerminal || outcome.ReasonCode != view.ReasonCode {
				return errors.New("alarmd execution: invalid State series requires matching TERMINAL Level outcome")
			}
			constrained = true
		}
	}
	guardReasons := loadedGuardReasons(plan.Identity, outcome.LevelID, stateIdentity, states, gaps)
	if len(guardReasons) != 0 {
		switch outcome.Outcome {
		case LevelOutcomeNormal, LevelOutcomeRecovery:
			if !loadedSeriesWarmingCompleted(outcome, plan, stateResults, states, gaps) {
				return errors.New("alarmd execution: active Runtime State or Plan gap guard forbids NORMAL and RECOVERY")
			}
		case LevelOutcomeUnknown:
			if !loadedSeriesWarmingCompleted(outcome, plan, stateResults, states, gaps) {
				if _, ok := guardReasons[outcome.ReasonCode]; !ok && disposition != PlanRetryPending {
					return errors.New("alarmd execution: UNKNOWN Level outcome does not preserve its active guard reason")
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
		return errors.New("alarmd execution: Level outcome lacks its EffectiveTime fact")
	}
	if effective.Fact.Status() == strategy.EffectiveTimeUnknown && !constrained {
		if outcome.Outcome != LevelOutcomeUnknown || outcome.ReasonCode != ReasonCode(contract.ReasonEffectiveTimeUnknown) {
			return errors.New("alarmd execution: UNKNOWN EffectiveTime requires matching UNKNOWN Level outcome")
		}
	}
	affected := affectedBindings(input, plan.Identity, outcome.LevelID)
	partial := make(map[RequirementID]NamedInputBinding)
	for _, binding := range affected {
		switch binding.Completeness {
		case CompletenessUnavailable:
			if binding.Disposition == AccessTerminal {
				if outcome.Outcome != LevelOutcomeTerminal {
					return errors.New("alarmd execution: terminal dependency requires TERMINAL Level outcome")
				}
			} else if outcome.Outcome != LevelOutcomeUnknown && outcome.Outcome != LevelOutcomeTerminal {
				return errors.New("alarmd execution: unavailable dependency cannot produce a business Level outcome")
			}
		case CompletenessPartial:
			partial[binding.RequirementID] = binding
		}
	}
	if len(partial) == 0 {
		if len(outcome.PartialProofs) != 0 {
			return errors.New("alarmd execution: FULL Level outcome must not carry PARTIAL proof receipts")
		}
		return nil
	}
	if outcome.Outcome != LevelOutcomeAbnormal {
		if outcome.Outcome == LevelOutcomeNormal || outcome.Outcome == LevelOutcomeRecovery {
			return errors.New("alarmd execution: PARTIAL input cannot produce NORMAL or RECOVERY")
		}
		if len(outcome.PartialProofs) != 0 {
			return errors.New("alarmd execution: non-ABNORMAL PARTIAL outcome must not carry proof receipts")
		}
		return nil
	}
	capability, ok := partialCapability(plan, outcome.LevelID)
	if !ok || capability.Policy != PartialProvableAbnormalOnly || capability.Proof == nil {
		return errors.New("alarmd execution: PARTIAL ABNORMAL Level lacks a registered proof capability")
	}
	proofs := make(map[RequirementID]PartialDecisionProof, len(outcome.PartialProofs))
	for _, proof := range outcome.PartialProofs {
		if _, duplicate := proofs[proof.RequirementID]; duplicate {
			return errors.New("alarmd execution: duplicate PARTIAL proof receipt")
		}
		binding, found := partial[proof.RequirementID]
		if !found || proof.DatasetName != binding.DatasetName || binding.PartialEvidence == nil ||
			proof.EvidenceDigest != binding.PartialEvidence.EvidenceDigest || proof.Result != PartialProofProvenAbnormal ||
			proof.Proof != *capability.Proof || !partialEvidenceSupports(capability, binding.PartialEvidence) {
			return errors.New("alarmd execution: PARTIAL proof receipt does not close its compiled evidence")
		}
		proofs[proof.RequirementID] = proof
	}
	if len(proofs) != len(partial) {
		return errors.New("alarmd execution: every PARTIAL input affecting ABNORMAL requires one proof receipt")
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
			return errors.New("alarmd execution: State mutation lacks its loaded view")
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
	loaded, found := states.Find(identity)
	if !found || loaded.Status != StateFoundWarming || loaded.SeriesGuard != nil {
		return false
	}
	matchingLoadedLevel := false
	for _, level := range loaded.Levels {
		if level.LevelID != outcome.LevelID {
			continue
		}
		if matchingLoadedLevel || level.HistoryCompleteness != HistoryWarming || level.GapReasonCode == "" {
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
	terminalReasons := make(map[ReasonCode]struct{})
	qualityReasons := make(map[ReasonCode]struct{})
	for _, binding := range affectedBindings(input, plan, outcome.LevelID) {
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
			return false, errors.New("alarmd execution: localized terminal input requires TERMINAL Level outcome")
		}
		if _, ok := terminalReasons[outcome.ReasonCode]; !ok {
			return false, errors.New("alarmd execution: localized terminal reason differs from Level outcome")
		}
		return true, nil
	}
	if len(qualityReasons) != 0 {
		if outcome.Outcome != LevelOutcomeUnknown {
			return false, errors.New("alarmd execution: localized quality fact requires UNKNOWN Level outcome")
		}
		if _, ok := qualityReasons[outcome.ReasonCode]; !ok {
			return false, errors.New("alarmd execution: localized quality reason differs from Level outcome")
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
	var bindings []NamedInputBinding
	for _, binding := range input.Inputs {
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
				return errors.New("alarmd execution: State mutation anchor has neither a business outcome nor an exact durable guard")
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
					return errors.New("alarmd execution: State Level fact lacks a matching Level outcome")
				}
				effectiveStatus := ""
				if effective, found := findEffectiveTimeFact(input, outcome.Plan, outcome.LevelID, outcome.SeriesIdentityDigest); found {
					effectiveStatus = effective.Fact.Status()
				}
				if !levelFactMatchesOutcome(fact.Result, outcome.Outcome) &&
					!stateFactMayAdvanceUnknown(
						fact.Result, outcome, state.Mutation, effectiveStatus, stateInputAllowsAdvance(input, outcome),
					) {
					return errors.New("alarmd execution: State Level fact contradicts its Level outcome")
				}
				identity := levelOutcomeIdentity{
					Plan: state.Mutation.Identity.Plan, LevelID: fact.LevelID,
					SeriesIdentityDigest: state.Mutation.Identity.SeriesIdentityDigest, Record: anchor,
				}
				if _, duplicate := facts[identity]; duplicate {
					return errors.New("alarmd execution: duplicate State fact for one Level outcome")
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
					return errors.New("alarmd execution: PARTIAL ABNORMAL State must preserve recovery warming/gap provenance")
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
			return errors.New("alarmd execution: successful Level outcome lacks its State fact")
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

// validateStateHistoryReplacement proves that Points is the complete bounded
// replacement snapshot obtained from the loaded history plus this batch's
// affected anchors. Retention may evict only the oldest points.
func validateStateHistoryReplacement(loaded []StateHistoryPoint, mutation StateMutation, retention uint32) error {
	if retention == 0 {
		return errors.New("alarmd execution: State history replacement lacks a retention bound")
	}
	affected := make(map[RecordAnchor]struct{}, len(mutation.AffectedRecords))
	for _, anchor := range mutation.AffectedRecords {
		affected[anchor] = struct{}{}
	}
	loadedPoints := make(map[RecordAnchor]StateHistoryPoint, len(loaded))
	allAnchors := make(map[RecordAnchor]struct{}, len(loaded)+len(affected))
	for _, point := range loaded {
		anchor := RecordAnchor{RecordID: point.RecordID, SourceTime: point.SourceTime}
		loadedPoints[anchor] = point
		allAnchors[anchor] = struct{}{}
	}
	if len(mutation.Points) != 0 {
		for anchor := range affected {
			allAnchors[anchor] = struct{}{}
		}
	}
	for _, point := range mutation.Points {
		anchor := RecordAnchor{RecordID: point.RecordID, SourceTime: point.SourceTime}
		if loadedPoint, ok := loadedPoints[anchor]; ok {
			if !reflect.DeepEqual(loadedPoint, point) {
				return errors.New("alarmd execution: State history replacement changes a loaded point")
			}
			continue
		}
		if _, current := affected[anchor]; !current {
			return errors.New("alarmd execution: State history replacement changes or invents a loaded point")
		}
	}
	expectedAnchors := make([]RecordAnchor, 0, len(allAnchors))
	for anchor := range allAnchors {
		expectedAnchors = append(expectedAnchors, anchor)
	}
	sort.Slice(expectedAnchors, func(left, right int) bool {
		if expectedAnchors[left].SourceTime != expectedAnchors[right].SourceTime {
			return expectedAnchors[left].SourceTime < expectedAnchors[right].SourceTime
		}
		return expectedAnchors[left].RecordID < expectedAnchors[right].RecordID
	})
	if len(expectedAnchors) > int(retention) {
		expectedAnchors = expectedAnchors[len(expectedAnchors)-int(retention):]
	}
	if len(expectedAnchors) != len(mutation.Points) {
		return errors.New("alarmd execution: State history replacement is not the complete bounded snapshot")
	}
	for index, point := range mutation.Points {
		anchor := RecordAnchor{RecordID: point.RecordID, SourceTime: point.SourceTime}
		if anchor != expectedAnchors[index] {
			return errors.New("alarmd execution: State history replacement is not the complete bounded snapshot")
		}
	}
	return nil
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

func stateInputAllowsAdvance(input InternalExecution, outcome LevelOutcome) bool {
	bindings := affectedBindings(input, outcome.Plan, outcome.LevelID)
	if len(bindings) == 0 {
		return false
	}
	for _, binding := range bindings {
		if binding.Completeness != CompletenessFull || binding.DataState != DataStateData ||
			binding.Disposition != AccessAvailable {
			return false
		}
	}
	localized, err := validateLocalizedInputOutcome(input, outcome.Plan, outcome)
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
					return errors.New("alarmd execution: PARTIAL ABNORMAL lacks its final Plan/Level gap guard")
				}
			}
			continue
		}
		if finalStateGuardsOutcome(finalStates, outcome) || finalGapGuardsOutcome(finalMarkers, outcome) {
			continue
		}
		return errors.New("alarmd execution: degraded Level outcome lacks an exact durable guard")
	}
	if obligations == 0 &&
		(result.Disposition == PlanUnavailable || result.Disposition == PlanReadinessGap || result.Disposition == PlanTerminal) &&
		!hasFinalPlanGap(finalMarkers) {
		return errors.New("alarmd execution: degraded plan completion requires a durable pre-event gap guard")
	}
	return nil
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
	for identity, outcome := range outcomes {
		effective, found := findEffectiveTimeFact(input, identity.Plan, identity.LevelID, identity.SeriesIdentityDigest)
		if !found {
			return errors.New("alarmd execution: TriggerEvent validation lacks EffectiveTime fact")
		}
		if effective.Fact.Status() != strategy.EffectiveTimeActive {
			continue
		}
		record := recordIdentity{Series: identity.SeriesIdentityDigest, Record: identity.Record}
		switch outcome.Outcome {
		case LevelOutcomeAbnormal:
			expectedEvents[record] = contract.TriggerEventAbnormal
		case LevelOutcomeRecovery:
			if expectedEvents[record] == "" {
				expectedEvents[record] = contract.TriggerEventRecovery
			}
		}
	}
	actualEvents := make(map[recordIdentity]struct{})
	for _, state := range result.StateResults {
		for _, event := range state.Events {
			record := recordIdentity{
				Series: state.Mutation.Identity.SeriesIdentityDigest,
				Record: RecordAnchor{RecordID: event.RecordRef.RecordID, SourceTime: event.RecordRef.SourceTime},
			}
			if _, duplicate := actualEvents[record]; duplicate {
				return errors.New("alarmd execution: duplicate TriggerEvent for one Plan series record")
			}
			if expectedEvents[record] == "" || event.EventKind != expectedEvents[record] {
				return errors.New("alarmd execution: TriggerEvent kind does not match Level outcomes")
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
					return errors.New("alarmd execution: TriggerEvent Level result contradicts Level outcome decomposition")
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
						return errors.New("alarmd execution: TriggerEvent omitted a successful sibling Level outcome")
					}
				}
			}
		}
	}
	if len(actualEvents) != len(expectedEvents) {
		return errors.New("alarmd execution: ABNORMAL or RECOVERY Level outcomes require exactly one TriggerEvent envelope")
	}
	for record := range expectedEvents {
		if _, found := actualEvents[record]; !found {
			return errors.New("alarmd execution: missing TriggerEvent for business Level outcome")
		}
	}
	return nil
}
