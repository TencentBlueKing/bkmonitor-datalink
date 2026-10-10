// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

func TestExcludedMembersAreAbsentFromAdmissionAndNoDataTogether(t *testing.T) {
	resolution := &targetplan.Resolution{Static: map[string]struct{}{"101": {}, "102": {}}, Excluded: map[string]struct{}{"101": {}},
		Selectors: []targetplan.SelectorResult{{Kind: targetplan.SelectorKindExclude, State: targetplan.SelectorOK, Reason: targetplan.ReasonExcludedAbsent, Kept: 1, Dropped: 2}}}
	resolution.Compose()
	target := newResolvedTarget(resolution)
	if target.Contains("101") || !target.Contains("102") || !target.Definitive() || !reflect.DeepEqual(target.absenceView().Members, []string{"102"}) {
		t.Fatalf("target views disagree: %+v", target)
	}
	if summary := target.summary("7"); summary.ExcludedAbsent != 2 || len(summary.Failures) != 0 {
		t.Fatalf("normal missing exclusions lost or became failures: %+v", summary)
	}
	encoded, err := json.Marshal(target.summary("7"))
	if err != nil {
		t.Fatal(err)
	}
	var restored fleet.RestoredTargetResolution
	if err := json.Unmarshal(encoded, &restored); err != nil || restored.ExcludedAbsent != 2 || len(restored.Failures) != 0 {
		t.Fatalf("normal exclusion evidence lost in the object read: %+v error %v", restored, err)
	}
	plan := admission.PlanContext{TargetPlan: &admission.TargetPlanContext{
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, Members: target}}
	facts := admission.Facts{Dimensions: map[string]json.RawMessage{"bk_host_id": json.RawMessage(`101`)}}
	decision := (admission.TargetPlanFilter{}).Admit(plan, &facts)
	if decision.Admit || !admission.DefinitelyOutside(plan, &facts, "target_plan", decision.Reason) {
		t.Fatalf("excluded member was admitted or scope-close lost the complete verdict: %+v", decision)
	}
	resolution.ExclusionUnavailable = true
	for _, reason := range []string{targetplan.ReasonModelUnresolved, targetplan.ReasonStale} {
		t.Run(reason, func(t *testing.T) {
			resolution.Selectors = []targetplan.SelectorResult{{Kind: targetplan.SelectorKindExclude, ID: "cw-Host", State: targetplan.SelectorUnavailable, Reason: reason}}
			resolution.Compose()
			target := newResolvedTarget(resolution)
			if target.Contains("102") || target.Definitive() || target.absenceView().State != nodata.TargetResolutionUnavailable || len(target.absenceView().Members) != 0 {
				t.Fatalf("unknown exclusion treated as an empty or complete target: %+v", target)
			}
			plan.TargetPlan.Members = target
			decision := (admission.TargetPlanFilter{}).Admit(plan, &facts)
			if decision.Admit || decision.Reason != admission.TargetPlanReasonSelectorUnavailable || admission.DefinitelyOutside(plan, &facts, "target_plan", decision.Reason) {
				t.Fatalf("failed exclusions admitted or closed a member: %+v", decision)
			}
		})
	}
}
