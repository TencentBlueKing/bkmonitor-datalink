// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package nodata

import (
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func hostTargetPlan(static ...string) *contract.TargetPlanV1 {
	keys := append([]string{}, static...)
	return &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleHostID,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: keys, DynamicGroups: []string{"1001"}}
}

func targetPlanSlotPlan(plan *contract.TargetPlanV1, dimensions []string) *contract.EvaluationPlanV2 {
	return &contract.EvaluationPlanV2{
		PlanID: "1001", TargetPlan: plan,
		NoData: &contract.NoDataConfigV1{Continuous: 3, Level: 2, AggDimension: dimensions},
	}
}

func hostIDGroup(t *testing.T, id string) Group {
	t.Helper()
	group, ok := Project(map[string]string{"bk_host_id": id}, []string{"bk_host_id"})
	if !ok {
		t.Fatalf("fixture: Project() rejected a host id series %s", id)
	}
	return group
}

// A Plan with a target plan is classified by the target plan and never by
// the scope it does not have: a nil scope beside a target plan would read as
// a history roster and the target would decide nothing. The roster can be
// enumerated exactly when the no-data dimensions are the key dimensions.
func TestATargetPlanIsClassifiedByItsKeyDimensionsAndNeverAsHistory(t *testing.T) {
	plan := hostTargetPlan("101")
	class, err := ClassifyTarget(nil, plan, []string{"bk_host_id"})
	if err != nil || class.Source != RosterTargetPlan || class.Plan != plan {
		t.Fatalf("ClassifyTarget(host id dims) = %+v, %v; want TARGET_PLAN with the plan", class, err)
	}
	for name, dimensions := range map[string][]string{
		"superset":            {"bk_host_id", "device_name"},
		"the old host pair":   {HostIPDimension, HostCloudDimension},
		"whole item":          {},
		"another dimension":   {"device_name"},
		"empty dimension":     {""},
		"duplicate host only": {"bk_host_id", "bk_host_id", "x"},
	} {
		t.Run(name, func(t *testing.T) {
			class, err := ClassifyTarget(nil, plan, dimensions)
			var unsupported *RosterUnsupportedError
			if !errors.As(err, &unsupported) {
				t.Fatalf("ClassifyTarget(%v) = %+v, %v; want refused by name, never history or whole", dimensions, class, err)
			}
		})
	}
	gated := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleModelInstID,
		Identity:   contract.TargetPlanIdentityV1{Dimensions: []string{"cw_object_model_inst_id"}, ModelDimension: "cw_object_model_id", ModelValue: "17"},
		StaticKeys: []string{"101"}}
	if class, err := ClassifyTarget(nil, gated, []string{"cw_object_model_inst_id", "cw_object_model_id"}); err != nil || class.Source != RosterTargetPlan {
		t.Fatalf("gated plan with both dimensions = %+v, %v", class, err)
	}
	if _, err := ClassifyTarget(nil, gated, []string{"cw_object_model_inst_id"}); err == nil {
		t.Fatal("gated plan without the model dimension in the no-data dimensions was classified")
	}
	// Without a target plan the old classification is untouched.
	if class, err := ClassifyTarget(nil, nil, []string{HostIPDimension, HostCloudDimension}); err != nil || class.Source != RosterHistory {
		t.Fatalf("no target at all = %+v, %v; want history as before", class, err)
	}
}

// The dependency check comes first, whatever the Slot saw. An unresolved or
// unavailable target pauses absence by name and moves nothing; dropped
// members pause it under their own name; a complete resolution is the
// roster and the round is judged against it.
func TestEvaluateSlotJudgesATargetPlanOnlyAgainstACompleteResolution(t *testing.T) {
	plan := targetPlanSlotPlan(hostTargetPlan("101"), []string{"bk_host_id"})
	absent := hostIDGroup(t, "102")
	memory := map[string]GroupMemory{absent.Key(): {FirstAbsent: 940}}
	for name, test := range map[string]struct {
		resolution *TargetResolution
		series     []map[string]string
		outcome    SlotOutcome
	}{
		"nothing resolved it":              {resolution: nil, outcome: OutcomeSkippedTargetSelectorUnavailable},
		"a selector unavailable":           {resolution: &TargetResolution{State: TargetResolutionUnavailable, Members: []string{"101"}}, outcome: OutcomeSkippedTargetSelectorUnavailable},
		"members dropped":                  {resolution: &TargetResolution{State: TargetResolutionIncomplete, Members: []string{"101", "102"}}, outcome: OutcomeSkippedTargetMembersDropped},
		"unavailable and no series at all": {resolution: &TargetResolution{State: TargetResolutionUnavailable}, series: nil, outcome: OutcomeSkippedTargetSelectorUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			result, outcome, err := EvaluateSlot(SlotInput{
				Plan: plan, EvaluationTime: 1000, PeriodSeconds: 60, Completeness: execution.CompletenessFull,
				Series: test.series, TargetResolution: test.resolution, Memory: memory,
			})
			if err != nil || outcome != test.outcome {
				t.Fatalf("EvaluateSlot() = %q, %v; want %q", outcome, err, test.outcome)
			}
			if len(result.Verdicts) != 0 || len(result.Memory) != 0 {
				t.Fatalf("verdicts %v memory %v; a paused round judges nothing and moves nothing", result.Verdicts, result.Memory)
			}
		})
	}

	// Complete: 101 static, 102 and 103 from the resolution; 102 arrives,
	// 101 and 103 do not, and the absence remembered on 102 closes.
	result, outcome, err := EvaluateSlot(SlotInput{
		Plan: plan, EvaluationTime: 1000, PeriodSeconds: 60, Completeness: execution.CompletenessFull,
		Series:           []map[string]string{{"bk_host_id": "102"}},
		TargetResolution: &TargetResolution{State: TargetResolutionComplete, Members: []string{"101", "102", "103"}},
		Memory:           memory,
	})
	if err != nil || outcome != OutcomeEvaluated {
		t.Fatalf("complete: EvaluateSlot() = %q, %v", outcome, err)
	}
	if result.Facts.RosterSource != RosterTargetPlan || result.Facts.Expected != 3 {
		t.Fatalf("facts = %+v, want a TARGET_PLAN roster of 3", result.Facts)
	}
	if got := result.Verdicts[hostIDGroup(t, "101").Key()]; got != VerdictAnomaly {
		t.Fatalf("101 verdict = %q, want %q: expected and silent", got, VerdictAnomaly)
	}
	if got := result.Verdicts[hostIDGroup(t, "103").Key()]; got != VerdictAnomaly {
		t.Fatalf("103 verdict = %q, want %q: a dynamic member is expected like a static one", got, VerdictAnomaly)
	}
	if got := result.Verdicts[absent.Key()]; got != VerdictNormal {
		t.Fatalf("102 verdict = %q, want %q: it arrived", got, VerdictNormal)
	}
	if _, whole := result.Verdicts[WholeItemGroup().Key()]; whole {
		t.Fatal("a target plan roster judged the whole item")
	}
}

// A complete resolution to no member is a confirmed-empty target: the
// absences open on members that left close exactly once, and the item is
// not reported absent as a whole - that reading belongs to the old target
// form and is not claimed here. The round after that has nothing to close
// and opens nothing.
func TestAConfirmedEmptyTargetPlanClosesOpenAbsencesOnceAndNeverTheWholeItem(t *testing.T) {
	plan := targetPlanSlotPlan(&contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleHostID,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{}, DynamicGroups: []string{"1001"}}, []string{"bk_host_id"})
	left := hostIDGroup(t, "102")
	memory := map[string]GroupMemory{left.Key(): {FirstAbsent: 940}, WholeItemGroup().Key(): {FirstAbsent: 900}}
	result, outcome, err := EvaluateSlot(SlotInput{
		Plan: plan, EvaluationTime: 1000, PeriodSeconds: 60, Completeness: execution.CompletenessFull,
		TargetResolution: &TargetResolution{State: TargetResolutionComplete}, Memory: memory,
	})
	if err != nil || outcome != OutcomeEvaluated {
		t.Fatalf("EvaluateSlot() = %q, %v; a complete empty answer is an answer", outcome, err)
	}
	if got := result.Verdicts[left.Key()]; got != VerdictNormal {
		t.Fatalf("verdict for the member that left = %q, want %q closed once", got, VerdictNormal)
	}
	// The stale whole-item absence an earlier build left is closed once,
	// like any absence the roster no longer carries; it is never opened.
	if got := result.Verdicts[WholeItemGroup().Key()]; got != VerdictNormal {
		t.Fatalf("stale whole-item verdict = %q, want %q closed once", got, VerdictNormal)
	}
	if len(result.Memory) != 0 {
		t.Fatalf("memory %v, want the closed absence and the stale whole-item entry gone", result.Memory)
	}
	again, outcome, err := EvaluateSlot(SlotInput{
		Plan: plan, EvaluationTime: 1060, PeriodSeconds: 60, Completeness: execution.CompletenessFull,
		TargetResolution: &TargetResolution{State: TargetResolutionComplete}, Memory: result.Memory,
	})
	if err != nil || outcome != OutcomeEvaluated || len(again.Verdicts) != 0 || len(again.Memory) != 0 {
		t.Fatalf("second round = %q %v verdicts %v memory %v; nothing left to close, nothing to open", outcome, err, again.Verdicts, again.Memory)
	}
}

// The two new outcomes are on the list a partition pre-creates from.
func TestTheTargetPlanOutcomesAreOnTheList(t *testing.T) {
	listed := map[SlotOutcome]bool{}
	for _, outcome := range SlotOutcomes {
		listed[outcome] = true
	}
	if !listed[OutcomeSkippedTargetSelectorUnavailable] || !listed[OutcomeSkippedTargetMembersDropped] {
		t.Fatalf("SlotOutcomes = %v", SlotOutcomes)
	}
}
