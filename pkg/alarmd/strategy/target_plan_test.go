// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package strategy

import (
	"context"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// A Plan carries its target plan through compilation unchanged and exposes
// it beside the scope; a Plan carrying both forms, or a target plan that
// does not validate, is a Plan-level terminal named at the field - never a
// Plan that runs on whichever form the worker read first, and never one
// that runs on no target.
func TestTheCompiledPlanCarriesTheTargetPlanAndRefusesTwoFormsOrABrokenOne(t *testing.T) {
	compiler := newTestCompiler(t)
	target := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleHostID,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{"101"}}
	plan := validPlan()
	plan.TargetPlan = target
	result, err := compiler.Compile(context.Background(), validRequest(plan))
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("terminal = %+v", result.PlanTerminal())
	}
	if !reflect.DeepEqual(compiled.TargetPlan(), target) || compiled.TargetScope() != nil {
		t.Fatalf("compiled target plan = %+v scope = %+v", compiled.TargetPlan(), compiled.TargetScope())
	}
	if view := compiled.NoDataView(); view != nil && view.TargetPlan() != target {
		t.Fatal("the no-data view lost the target plan")
	}

	both := validPlan()
	both.TargetPlan = target
	both.TargetScope = &contract.TargetScopeV2{Groups: []contract.TargetScopeGroupV2{{Conditions: []contract.TargetScopeConditionV2{{
		Field: contract.TargetScopeHost, Method: contract.TargetScopeInclude, Keys: []string{"101"}}}}}}
	result, err = compiler.Compile(context.Background(), validRequest(both))
	if err != nil {
		t.Fatal(err)
	}
	if terminal := result.PlanTerminal(); terminal == nil || terminal.ReasonCode != contract.ReasonPlanInvalid || terminal.FieldPath != "target_plan" {
		t.Fatalf("two forms: terminal = %+v, want PLAN_INVALID at target_plan", terminal)
	}

	broken := validPlan()
	broken.TargetPlan = &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: "service_instance",
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"x"}}, StaticKeys: []string{"1"}}
	result, err = compiler.Compile(context.Background(), validRequest(broken))
	if err != nil {
		t.Fatal(err)
	}
	if terminal := result.PlanTerminal(); terminal == nil || terminal.ReasonCode != contract.ReasonPlanInvalid || terminal.FieldPath != "target_plan" {
		t.Fatalf("broken form: terminal = %+v, want PLAN_INVALID at target_plan", terminal)
	}
}
