// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker_test

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
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
