// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package access

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

type memberSet map[string]struct{}

func (set memberSet) Contains(key string) bool { _, found := set[key]; return found }

func hostIDSeries(t *testing.T, id string) *execution.Dataset {
	t.Helper()
	return execution.NewDataset([]contract.CanonicalRecordV2{{
		RecordID: "record", SourceTime: 1, BusinessID: "2",
		Dimensions: map[string]json.RawMessage{"bk_host_id": json.RawMessage(`"` + id + `"`)},
	}})
}

// The plan context of a Plan carrying a target plan carries the target plan
// with the execution's resolution for it, and no scope; with no resolution
// the members stay nil and the filter admits nothing - never everything, as
// a Plan with no scope would. A Plan without a target plan is unchanged.
func TestPlanScopesCarryTheTargetPlanAndTheExecutionsResolution(t *testing.T) {
	target := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleHostID,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{"101"}}
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "2002"}
	due := []execution.DuePlan{{Identity: identity, CompiledPlan: compilePlanWithTargetPlan(t, "2002", target)}}

	unresolved := buildPlanScopes(due, nil)
	context := unresolved[identity]
	if context.TargetScope != nil || context.TargetPlan == nil || context.TargetPlan.Members != nil {
		t.Fatalf("unresolved context = %+v, want the target plan with nil members and no scope", context)
	}
	chain := admission.NewChain([]admission.Fuller{admission.IdentityFuller{}}, []admission.Filter{admission.TargetScopeFilter{}, admission.TargetPlanFilter{}})
	facts := chain.Enrich(map[string]json.RawMessage{"bk_host_id": json.RawMessage(`"101"`)})
	if admitted, filter, reason := chain.Admit(context, &facts); admitted || filter != "target_plan" || reason != admission.TargetPlanReasonUnresolved {
		t.Fatalf("an unresolved target plan admitted a record: %v %s %s", admitted, filter, reason)
	}

	resolved := buildPlanScopes(due, execution.TargetMemberships{identity: memberSet{"101": {}}})
	context = resolved[identity]
	if context.TargetPlan == nil || context.TargetPlan.Members == nil {
		t.Fatalf("resolved context = %+v, want the members", context)
	}
	if admitted, _, _ := chain.Admit(context, &facts); !admitted {
		t.Fatal("a resolved member was not admitted")
	}
	// A nil entry in the memberships is unresolved, not empty.
	nilEntry := buildPlanScopes(due, execution.TargetMemberships{identity: nil})
	if nilEntry[identity].TargetPlan.Members != nil {
		t.Fatal("a nil membership entry became a resolution")
	}
}

// Through the adapter: a target-plan Plan's series are admitted against the
// resolution the consumer handed the source, by the record's own bk_host_id,
// and counted under the target_plan filter.
func TestATargetPlanSeriesIsAdmittedAgainstTheConsumersResolution(t *testing.T) {
	_, frozen := frozenExecution(t)
	requirement := frozen.Requirements[0]
	plan := requirement.Consumers[0].Consumer.Plan
	identity := contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}

	var decisions []string
	consumer := &admissionConsumer{}
	adapter := &seriesAdapter{
		consumer: consumer, query: plannedQueryForTest(requirement), attemptNo: 1,
		admission: admission.NewChain([]admission.Fuller{admission.IdentityFuller{}}, []admission.Filter{admission.TargetScopeFilter{}, admission.TargetPlanFilter{}}),
		observe:   func(filter, result, reason string) { decisions = append(decisions, filter+"/"+result+"/"+reason) },
		scopes:    planScopes{plan: {StrategyID: plan.StrategyID, TargetPlan: &admission.TargetPlanContext{Identity: identity, Members: memberSet{"101": {}}}}},
	}
	deliver := func(dataset *execution.Dataset) {
		t.Helper()
		if err := adapter.ConsumeProviderSeries(context.Background(), execution.ProviderSeriesBatch{
			PhysicalQuery: adapter.query.Spec.Digest, CompletionRef: "result", Dataset: dataset,
			Delivery: execution.SeriesDelivery{PhysicalQuery: adapter.query.Spec.Digest,
				QueryRevision: adapter.query.Spec.PlanFacts.QueryRevision, Series: 1, Records: 1, Digest: "digest"},
		}); err != nil {
			t.Fatalf("consume: %v", err)
		}
	}
	deliver(hostIDSeries(t, "101"))
	deliver(hostIDSeries(t, "102"))
	deliver(hostSeries(t, "192.0.2.1"))
	if len(consumer.batches) != 1 {
		t.Fatalf("delivered %d batches, want only the member's series", len(consumer.batches))
	}
	if len(decisions) != 3 || decisions[0] != "target_plan/admitted/in_target" || decisions[1] != "target_plan/rejected/out_of_target" || decisions[2] != "target_plan/rejected/target_key_missing" {
		t.Fatalf("decisions = %v", decisions)
	}
}
