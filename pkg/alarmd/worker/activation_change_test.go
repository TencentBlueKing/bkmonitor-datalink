// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Slot whose activated Plans changed under it completes as a partial gap
// under one of three words, told apart from one comparison: an edit is
// CONFIG_DRIFT; the same Plan with only its activation epoch moved came back
// after leaving, PLAN_REACTIVATED; a Plan the activation no longer names is
// gone, PLAN_NOT_ACTIVE. On one deployment a strategy list that dropped
// entries for minutes every hour had 22 Plans removed and re-added hourly,
// and both the Slot that ran without the Plan and the Slot that ran with it
// back were written off as configuration drift, for a configuration that
// never changed.
func TestActivationChangeIsClassifiedFromTheSameComparisonAsDrift(t *testing.T) {
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1001"}
	due := execution.DuePlan{Identity: identity, StateGeneration: "gen-1", StateApplyEpoch: 7, ScheduleRevision: "sched-1"}
	other := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1002"}
	otherDue := execution.DuePlan{Identity: other, StateGeneration: "gen-1", StateApplyEpoch: 7, ScheduleRevision: "sched-1"}
	selected := func(plan execution.ActivatedPlan) execution.PlanActivationFact {
		return execution.PlanActivationFact{Plan: plan.Identity, Selection: execution.ActivationCurrent, Selected: plan}
	}
	same := execution.ActivatedPlan{Identity: identity, StateGeneration: "gen-1", StateApplyEpoch: 7, ScheduleRevision: "sched-1"}
	returned := same
	returned.StateApplyEpoch = 9
	rewarmed := same
	rewarmed.ForceWarming = true
	edited := same
	edited.ScheduleRevision = "sched-2"
	regenerated := same
	regenerated.StateGeneration = "gen-2"
	regenerated.StateApplyEpoch = 9
	result := func(facts ...execution.PlanActivationFact) execution.PlanActivationResult {
		return execution.PlanActivationResult{Facts: facts}
	}
	// The Guards loaded for the Slot: a marker for the frozen generation
	// means that generation has run before.
	seen := execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
		Identity: execution.PlanGapIdentity{Plan: identity, StateGeneration: "gen-1"}, Status: execution.GapFound,
	}}}
	unseen := execution.GapLoadResult{}
	for _, testCase := range []struct {
		name        string
		duePlans    []execution.DuePlan
		activations execution.PlanActivationResult
		gaps        execution.GapLoadResult
		want        activationChange
		wantReason  string
		wantCause   execution.CompletionCause
	}{
		{name: "unchanged", duePlans: []execution.DuePlan{due}, activations: result(selected(same)),
			want: activationChangeUnchanged, wantReason: contract.ReasonConfigDrift, wantCause: execution.CauseConfigDrift},
		{name: "the Plan came back: only the activation epoch moved", duePlans: []execution.DuePlan{due}, activations: result(selected(returned)),
			want: activationChangeReactivated, wantReason: contract.ReasonPlanReactivated, wantCause: execution.CausePlanReactivated},
		{name: "the Plan came back: warming restarted on a generation the store holds a marker for", duePlans: []execution.DuePlan{due}, activations: result(selected(rewarmed)), gaps: seen,
			want: activationChangeReactivated, wantReason: contract.ReasonPlanReactivated, wantCause: execution.CausePlanReactivated},
		// The same restart on a generation no marker has seen is a new
		// generation: the edit itself, frozen after it was made.
		{name: "the Plan was edited: warming restarted on a generation the store has never held", duePlans: []execution.DuePlan{due}, activations: result(selected(rewarmed)), gaps: unseen,
			want: activationChangeDrift, wantReason: contract.ReasonConfigDrift, wantCause: execution.CauseConfigDrift},
		{name: "the Plan is gone: the activation does not name it", duePlans: []execution.DuePlan{due}, activations: result(),
			want: activationChangeNotActive, wantReason: contract.ReasonPlanNotActive, wantCause: execution.CausePlanNotActive},
		{name: "the Plan is gone: named but unselected", duePlans: []execution.DuePlan{due},
			activations: result(execution.PlanActivationFact{Plan: identity, Selection: execution.ActivationNone}),
			want:        activationChangeNotActive, wantReason: contract.ReasonPlanNotActive, wantCause: execution.CausePlanNotActive},
		{name: "the Plan was edited: schedule revision moved", duePlans: []execution.DuePlan{due}, activations: result(selected(edited)),
			want: activationChangeDrift, wantReason: contract.ReasonConfigDrift, wantCause: execution.CauseConfigDrift},
		{name: "the Plan was edited: state generation moved with the epoch", duePlans: []execution.DuePlan{due}, activations: result(selected(regenerated)),
			want: activationChangeDrift, wantReason: contract.ReasonConfigDrift, wantCause: execution.CauseConfigDrift},
		// Several due Plans: the one a reader has to look at wins.
		{name: "an edit beside a return is drift", duePlans: []execution.DuePlan{due, otherDue},
			activations: result(selected(returned), selected(execution.ActivatedPlan{Identity: other, StateGeneration: "gen-1", StateApplyEpoch: 7, ScheduleRevision: "sched-9"})),
			want:        activationChangeDrift, wantReason: contract.ReasonConfigDrift, wantCause: execution.CauseConfigDrift},
		{name: "a gone Plan beside a returned one is not active", duePlans: []execution.DuePlan{due, otherDue},
			activations: result(selected(returned)),
			want:        activationChangeNotActive, wantReason: contract.ReasonPlanNotActive, wantCause: execution.CausePlanNotActive},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			change := classifyActivationChange(testCase.duePlans, testCase.activations, testCase.gaps)
			if change != testCase.want {
				t.Fatalf("change = %d, want %d", change, testCase.want)
			}
			primary := execution.PrimaryInputFact{Completeness: execution.CompletenessFull}
			completion, cause := activationChangeCompletion(execution.FrozenExecutionContractRef{}, &primary, change)
			if completion.Kind != execution.CompletionPartialGap || string(completion.ReasonCode) != testCase.wantReason || cause != testCase.wantCause {
				t.Fatalf("completion = (%s, %s, %s), want a partial gap under %s / %s", completion.Kind, completion.ReasonCode, cause, testCase.wantReason, testCase.wantCause)
			}
			// The unavailable-primary rule is the same under every word.
			unavailable := execution.PrimaryInputFact{Completeness: execution.CompletenessUnavailable}
			if completion, cause := activationChangeCompletion(execution.FrozenExecutionContractRef{}, &unavailable, change); completion.Kind != execution.CompletionUnavailable || cause != execution.CausePrimaryInputUnavailable {
				t.Fatalf("with an unavailable primary the completion is (%s, %s), want unavailable / primary input unavailable", completion.Kind, cause)
			}
		})
	}
}

// On a Slot where one Plan is gone and another came back, the Slot's word is
// PLAN_NOT_ACTIVE but the Guard is written only for the Plan that came back,
// and its word is that Plan's: PLAN_REACTIVATED. The Slot's word on that
// Guard would misname it, and it is not a Guard scope word at all - the
// fold ranks an unlisted word first and the page has no words for it.
func TestTheGuardWordIsClassifiedOverThePlansGettingAGuard(t *testing.T) {
	gone := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1001"}
	back := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1002"}
	duePlans := []execution.DuePlan{
		{Identity: gone, StateGeneration: "gen-1", StateApplyEpoch: 7, ScheduleRevision: "sched-1"},
		{Identity: back, StateGeneration: "gen-1", StateApplyEpoch: 7, ScheduleRevision: "sched-1"},
	}
	returned := execution.PlanActivationResult{Facts: []execution.PlanActivationFact{{
		Plan: back, Selection: execution.ActivationCurrent,
		Selected: execution.ActivatedPlan{Identity: back, StateGeneration: "gen-1", StateApplyEpoch: 9, ScheduleRevision: "sched-1"},
	}}}
	if change := classifyActivationChange(duePlans, returned, execution.GapLoadResult{}); change != activationChangeNotActive {
		t.Fatalf("the Slot's change = %d, want the gone Plan's PLAN_NOT_ACTIVE to outrank the returned one", change)
	}
	if reason := guardReasonFor(duePlans, returned, execution.GapLoadResult{}); string(reason) != contract.ReasonPlanReactivated {
		t.Fatalf("the Guard's word = %s, want %s: only the Plan that came back gets a Guard", reason, contract.ReasonPlanReactivated)
	}
	// And the Guard's word is a word the fold ranks and the scope vocabulary
	// names, which the Slot's word here is not.
	if contract.NormalizeGapScopeReason(contract.ReasonPlanReactivated) != contract.ReasonPlanReactivated {
		t.Fatalf("%s is not a Guard scope word", contract.ReasonPlanReactivated)
	}
	if contract.NormalizeGapScopeReason(contract.ReasonPlanNotActive) == contract.ReasonPlanNotActive {
		t.Fatalf("%s is a Guard scope word; the case assumes it is not", contract.ReasonPlanNotActive)
	}
}
