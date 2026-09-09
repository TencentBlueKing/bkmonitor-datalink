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

type admissionConsumer struct {
	execution.QueryExecutionConsumer
	batches []execution.SeriesExecutionBatch
}

func (consumer *admissionConsumer) ConsumeSeries(_ context.Context, batch execution.SeriesExecutionBatch) error {
	consumer.batches = append(consumer.batches, batch)
	return nil
}

func hostSeries(t *testing.T, address string) *execution.Dataset {
	t.Helper()
	return execution.NewDataset([]contract.CanonicalRecordV2{{
		RecordID: "record", SourceTime: 1, BusinessID: "2",
		Dimensions: map[string]json.RawMessage{
			"bk_target_ip":       json.RawMessage(`"` + address + `"`),
			"bk_target_cloud_id": json.RawMessage(`0`),
		},
	}})
}

func scopedChain() *admission.Chain {
	return admission.NewChain([]admission.Fuller{admission.IdentityFuller{}}, []admission.Filter{admission.TargetScopeFilter{}})
}

func hostScope(addresses ...string) *admission.TargetScope {
	keys := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		keys[address] = struct{}{}
	}
	return &admission.TargetScope{Groups: []admission.TargetScopeGroup{{
		Conditions: []admission.TargetScopeCondition{{
			Field: admission.TargetScopeHost, Method: admission.TargetScopeInclude, Keys: keys,
		}},
	}}}
}

// A series outside the strategy's target must not become one of its inputs -
// it must not reach State, evaluation or events. When it is outside every plan
// the query feeds, nothing is delivered at all.
func TestASeriesOutsideEveryTargetIsNotDelivered(t *testing.T) {
	_, frozen := frozenExecution(t)
	requirement := frozen.Requirements[0]
	plan := requirement.Consumers[0].Consumer.Plan

	consumer := &admissionConsumer{}
	var decisions []string
	adapter := &seriesAdapter{
		consumer:  consumer,
		query:     PlannedQuery{Requirements: []execution.DataRequirement{requirement}},
		attemptNo: 1,
		admission: scopedChain(),
		observe:   func(filter, result, reason string) { decisions = append(decisions, filter+"/"+result+"/"+reason) },
		scopes:    planScopes{plan: {StrategyID: plan.StrategyID, TargetScope: hostScope("10.0.0.1|0")}},
	}

	dataset := hostSeries(t, "10.9.9.9")
	err := adapter.ConsumeProviderSeries(context.Background(), execution.ProviderSeriesBatch{
		PhysicalQuery: adapter.query.Spec.Digest, CompletionRef: "result", Dataset: dataset,
		Delivery: execution.SeriesDelivery{PhysicalQuery: adapter.query.Spec.Digest, Series: 1, Records: 1, Digest: "digest"},
	})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(consumer.batches) != 0 {
		t.Fatalf("an out-of-scope series was delivered: %+v", consumer.batches)
	}
	if len(decisions) != 1 || decisions[0] != "target_scope/rejected/out_of_scope" {
		t.Fatalf("decisions = %v", decisions)
	}
}

// The same series, inside the target, is delivered unchanged.
func TestASeriesInsideTheTargetIsDeliveredUnchanged(t *testing.T) {
	_, frozen := frozenExecution(t)
	requirement := frozen.Requirements[0]
	plan := requirement.Consumers[0].Consumer.Plan

	consumer := &admissionConsumer{}
	adapter := &seriesAdapter{
		consumer:  consumer,
		query:     PlannedQuery{Requirements: []execution.DataRequirement{requirement}},
		attemptNo: 1,
		admission: scopedChain(),
		scopes:    planScopes{plan: {StrategyID: plan.StrategyID, TargetScope: hostScope("10.0.0.1|0")}},
	}

	dataset := hostSeries(t, "10.0.0.1")
	err := adapter.ConsumeProviderSeries(context.Background(), execution.ProviderSeriesBatch{
		PhysicalQuery: adapter.query.Spec.Digest, CompletionRef: "result", Dataset: dataset,
		Delivery: execution.SeriesDelivery{PhysicalQuery: adapter.query.Spec.Digest, Series: 1, Records: 1, Digest: "digest"},
	})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(consumer.batches) != 1 || len(consumer.batches[0].Inputs) == 0 {
		t.Fatalf("an in-scope series was not delivered: %+v", consumer.batches)
	}
}

// A query feeding two strategies delivers the series only to the one whose
// target includes it: the decision is per plan, not per query.
func TestOneSeriesIsAdmittedPerPlanNotPerQuery(t *testing.T) {
	_, frozen := frozenExecution(t)
	requirement := frozen.Requirements[0]
	included := requirement.Consumers[0].Consumer.Plan
	excludedConsumer := requirement.Consumers[0]
	excludedConsumer.Consumer.Plan.StrategyID = included.StrategyID + "-other"
	requirement.Consumers = append(requirement.Consumers, excludedConsumer)
	excluded := excludedConsumer.Consumer.Plan

	consumer := &admissionConsumer{}
	adapter := &seriesAdapter{
		consumer:  consumer,
		query:     PlannedQuery{Requirements: []execution.DataRequirement{requirement}},
		attemptNo: 1,
		admission: scopedChain(),
		scopes: planScopes{
			included: {StrategyID: included.StrategyID, TargetScope: hostScope("10.0.0.1|0")},
			excluded: {StrategyID: excluded.StrategyID, TargetScope: hostScope("10.0.0.2|0")},
		},
	}

	err := adapter.ConsumeProviderSeries(context.Background(), execution.ProviderSeriesBatch{
		PhysicalQuery: adapter.query.Spec.Digest, CompletionRef: "result", Dataset: hostSeries(t, "10.0.0.1"),
		Delivery: execution.SeriesDelivery{PhysicalQuery: adapter.query.Spec.Digest, Series: 1, Records: 1, Digest: "digest"},
	})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(consumer.batches) != 1 {
		t.Fatalf("batches = %d", len(consumer.batches))
	}
	for _, binding := range consumer.batches[0].Inputs {
		if binding.Consumer.Plan == excluded {
			t.Fatal("the series was bound to a plan whose target excludes it")
		}
	}
	if len(consumer.batches[0].Inputs) == 0 {
		t.Fatal("the series was not bound to the plan whose target includes it")
	}
}

// Without an admission gate the access path behaves exactly as before, so the
// filter can be absent only by saying so rather than by accident.
func TestNoAdmissionGateDeliversEverySeries(t *testing.T) {
	_, frozen := frozenExecution(t)
	requirement := frozen.Requirements[0]
	consumer := &admissionConsumer{}
	adapter := &seriesAdapter{
		consumer:  consumer,
		query:     PlannedQuery{Requirements: []execution.DataRequirement{requirement}},
		attemptNo: 1,
	}
	err := adapter.ConsumeProviderSeries(context.Background(), execution.ProviderSeriesBatch{
		PhysicalQuery: adapter.query.Spec.Digest, CompletionRef: "result", Dataset: hostSeries(t, "10.9.9.9"),
		Delivery: execution.SeriesDelivery{PhysicalQuery: adapter.query.Spec.Digest, Series: 1, Records: 1, Digest: "digest"},
	})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(consumer.batches) != 1 {
		t.Fatalf("batches = %d, want the series delivered unfiltered", len(consumer.batches))
	}
}

// The frozen contract's scope reaches the filter through the compiled plan, so
// a plan compiled with a target actually filters at runtime.
func TestPlanScopesComeFromTheCompiledPlan(t *testing.T) {
	_, frozen := frozenExecution(t)
	scopes := buildPlanScopes(frozen.DuePlans)
	if len(scopes) != len(frozen.DuePlans) {
		t.Fatalf("scopes = %d, due plans = %d", len(scopes), len(frozen.DuePlans))
	}
	for identity, plan := range scopes {
		if plan.StrategyID != identity.StrategyID || plan.BusinessID != identity.BusinessID {
			t.Fatalf("plan context %+v does not describe %+v", plan, identity)
		}
	}
}
