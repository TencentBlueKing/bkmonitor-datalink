// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// A Slot whose Plan carries a target plan resolves it at Begin - here on a
// worker with no resolver, so by the source's name - and the round it
// commits carries the resolution: one target_resolved line, and one
// summary on the progress commit's completion, so the object page can say
// what the Plan's records were filtered against.
func TestASlotResolvesItsTargetPlansAtBeginAndCommitsTheSummary(t *testing.T) {
	plans, requirements := baseDuePlanAndRequirements()
	plans[0].CompiledPlan = compiledPlanWithTargetForTest(t, plans[0].Identity.StrategyID, &contract.TargetPlanV1{
		SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleHostID,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{"101"}, DynamicGroups: []string{"1001"},
	})
	fixture, request := newCompletionOnlyFixture(t, plans, requirements, nil)
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	resolved := 0
	for _, observation := range *fixture.observations {
		if observation.Stage != observability.StageTargetResolved || observation.TargetResolution == nil {
			continue
		}
		resolved++
		facts := observation.TargetResolution
		if facts.StrategyID != plans[0].Identity.StrategyID || facts.State != string(targetplan.ResolutionUnavailable) ||
			len(facts.Selectors) != 1 || facts.Selectors[0].Reason != targetplan.ReasonSourceUnwired {
			t.Fatalf("target_resolved facts = %+v, want unavailable by the unwired source's name", facts)
		}
	}
	if resolved != 1 {
		t.Fatalf("target_resolved lines = %d, want one for the one target-plan Plan", resolved)
	}
	summaries := fixture.ports.lastProgress.Completion.TargetResolutions
	if len(summaries) != 1 || summaries[0].StrategyID != plans[0].Identity.StrategyID || summaries[0].State != string(targetplan.ResolutionUnavailable) ||
		len(summaries[0].Failures) != 1 || summaries[0].Failures[0].Reason != targetplan.ReasonSourceUnwired {
		t.Fatalf("committed summaries = %+v, want the one resolution on the round", summaries)
	}
}

// A Slot whose activation changed under it completes as the change's partial
// gap, and the targets it resolved at Begin are still its completion's: the
// drift completion replaced the one carrying them and dropped them, so the
// committed LastCompletion - what an object restored after a restart shows -
// said the Slot resolved nothing. Read back through the progress store.
func TestADriftSlotKeepsItsTargetResolutionsOnTheCommittedCompletion(t *testing.T) {
	plans, requirements := baseDuePlanAndRequirements()
	plans[0].CompiledPlan = compiledPlanWithTargetForTest(t, plans[0].Identity.StrategyID, &contract.TargetPlanV1{
		SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleHostID,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{"101"}, DynamicGroups: []string{"1001"},
	})
	// The change met at the guard's read, before the Slot's writes, and at
	// the read after them, which the convergence commits.
	for _, changeAt := range []int{1, 2} {
		driftKeepsTargets(t, plans, requirements, changeAt)
	}
}

func driftKeepsTargets(t *testing.T, plans []execution.DuePlan, requirements []execution.DataRequirement, changeAt int) {
	t.Helper()
	fixture, request := newCompletionOnlyFixture(t, plans, requirements, nil)
	fixture.ports.activationChangeAt = changeAt
	fixture.ports.activationSelection = execution.ActivationNone
	wantReason := contract.ReasonPlanNotActive
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || fixture.ports.lastProgress.Completion.Kind != execution.CompletionPartialGap ||
		fixture.ports.lastProgress.Completion.ReasonCode != execution.ReasonCode(wantReason) {
		t.Fatalf("change at read %d: Execute() result=%+v error=%v completion=%+v, want the %s partial gap",
			changeAt, result, err, fixture.ports.lastProgress.Completion, wantReason)
	}
	now := time.Unix(1_788_000_100, 0)
	store, err := progress.NewStore(progress.StoreOptions{Prefix: "alarmd", Control: &rangeControl{}, Slots: rangeSlots{}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	// The request the coordinator committed, into a store that holds no
	// Begin of it: without the unfinished Slot the projection would name, it
	// is committed as a first Progress, which writes LastCompletion the same.
	committed := fixture.ports.lastProgress
	committed.Projection = execution.UnfinishedSlotProjection{}
	if commit, err := store.CommitProgress(context.Background(), committed); err != nil || commit.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress() = (%+v, %v)", commit, err)
	}
	loaded, err := store.LoadProgress(context.Background(), committed.Identity)
	if err != nil || loaded.Progress == nil || loaded.Progress.LastCompletion == nil {
		t.Fatalf("LoadProgress() = (%+v, %v)", loaded, err)
	}
	summaries := loaded.Progress.LastCompletion.TargetResolutions
	if len(summaries) != 1 || summaries[0].StrategyID != plans[0].Identity.StrategyID || summaries[0].State != string(targetplan.ResolutionUnavailable) {
		t.Fatalf("change at read %d: committed LastCompletion target resolutions = %+v, want the one the Slot resolved", changeAt, summaries)
	}
}
