// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution_test

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestScheduleSpecRevisionIsStableAndComplete(t *testing.T) {
	base := execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Alignment: 0, Timezone: "UTC"}
	baseRevision := mustBaselinePlanRevision(t, base)
	if again := mustBaselinePlanRevision(t, base); again != baseRevision {
		t.Fatalf("stable revision = %q, want %q", again, baseRevision)
	}
	for _, changed := range []execution.ScheduleSpec{
		{EvaluationIntervalSeconds: 120, Alignment: 0, Timezone: "UTC"},
		{EvaluationIntervalSeconds: 60, Alignment: 30, Timezone: "UTC"},
		{EvaluationIntervalSeconds: 60, Alignment: 0, Timezone: "Asia/Shanghai"},
	} {
		if revision := mustBaselinePlanRevision(t, changed); revision == baseRevision {
			t.Fatalf("revision = %q after ScheduleSpec changed", revision)
		}
	}
}

func TestScheduleSpecRejectsInvalidInputs(t *testing.T) {
	for _, spec := range []execution.ScheduleSpec{
		{EvaluationIntervalSeconds: 0, Alignment: 0, Timezone: "UTC"},
		{EvaluationIntervalSeconds: 60, Alignment: -1, Timezone: "UTC"},
		{EvaluationIntervalSeconds: 60, Alignment: 60, Timezone: "UTC"},
		{EvaluationIntervalSeconds: 60, Alignment: 0, Timezone: ""},
		{EvaluationIntervalSeconds: 60, Alignment: 0, Timezone: "not/a-timezone"},
	} {
		if _, err := execution.DerivePlanScheduleRevision(spec); err == nil {
			t.Fatalf("DerivePlanScheduleRevision(%+v) error = nil", spec)
		}
	}
}

func TestQueryGroupScheduleRevisionIgnoresPlanOrder(t *testing.T) {
	firstSpec := execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Alignment: 0, Timezone: "UTC"}
	secondSpec := execution.ScheduleSpec{EvaluationIntervalSeconds: 120, Alignment: 0, Timezone: "UTC"}
	first := execution.FrozenPlanSchedule{Identity: schedulePlan("1"), ScheduleRevision: mustBaselinePlanRevision(t, firstSpec), Spec: firstSpec}
	second := execution.FrozenPlanSchedule{Identity: schedulePlan("2"), ScheduleRevision: mustBaselinePlanRevision(t, secondSpec), Spec: secondSpec}
	forward, err := execution.DeriveQueryGroupScheduleRevision([]execution.FrozenPlanSchedule{first, second})
	if err != nil {
		t.Fatalf("DeriveQueryGroupScheduleRevision() error = %v", err)
	}
	reverse, err := execution.DeriveQueryGroupScheduleRevision([]execution.FrozenPlanSchedule{second, first})
	if err != nil {
		t.Fatalf("DeriveQueryGroupScheduleRevision(reverse) error = %v", err)
	}
	if forward != reverse {
		t.Fatalf("revisions differ by Plan order: forward=%q reverse=%q", forward, reverse)
	}
}

func TestFrozenSlotContractBindsExactDueRefs(t *testing.T) {
	plans, requirements := baseDuePlanAndRequirements()
	spec := execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Alignment: 0, Timezone: "UTC"}
	plans[0].ScheduleSpec = spec
	plans[0].ScheduleRevision = mustBaselinePlanRevision(t, spec)
	evaluationTime := execution.EvaluationTime(1_788_000_000)
	plans[0].CompletionDeadlineUnixMilli = (int64(evaluationTime) + spec.EvaluationIntervalSeconds) * 1000
	scheduleRevision, err := execution.DeriveQueryGroupScheduleRevision([]execution.FrozenPlanSchedule{{
		Identity: plans[0].Identity, ScheduleRevision: plans[0].ScheduleRevision, Spec: spec,
	}})
	if err != nil {
		t.Fatalf("DeriveQueryGroupScheduleRevision() error = %v", err)
	}
	request := execution.FreezeSlotContractRequest{
		QueryGroup: "query-group", ScheduleRevision: scheduleRevision, ScheduleSegmentStart: evaluationTime - 60,
		EvaluationTime: evaluationTime,
		DuePlans:       []execution.FrozenPlanScheduleRef{{Identity: plans[0].Identity, ScheduleRevision: plans[0].ScheduleRevision}},
	}
	digest, err := execution.DeriveDuePlanSetDigest(plans, requirements)
	if err != nil {
		t.Fatalf("DeriveDuePlanSetDigest() error = %v", err)
	}
	fact := execution.FrozenSlotContractFact{
		Contract: execution.FrozenExecutionContractRef{
			Slot:             execution.SlotIdentity{QueryGroup: request.QueryGroup, EvaluationTime: request.EvaluationTime},
			SnapshotRevision: "snapshot-v1", QueryRevision: "query-v1", ScheduleRevision: request.ScheduleRevision,
			ScheduleSegmentStart: request.ScheduleSegmentStart, DuePlanSetDigest: digest,
		},
		DuePlans: plans, Requirements: requirements,
	}
	if err := fact.Validate(request); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	drifted := request
	drifted.DuePlans = []execution.FrozenPlanScheduleRef{{Identity: schedulePlan("other"), ScheduleRevision: plans[0].ScheduleRevision}}
	if err := fact.Validate(drifted); err == nil {
		t.Fatal("Validate() accepted different exact due Plan refs")
	}
}

func TestScheduleContractsExcludeRuntimeSelectorsAndOwnership(t *testing.T) {
	assertScheduleFields(t, reflect.TypeOf(execution.InitialScheduleActivationFact{}), []string{"Segment"})
	assertScheduleFields(t, reflect.TypeOf(execution.ScheduleCutoverFact{}), []string{"OldSegment", "NewSegment"})
	assertScheduleFields(t, reflect.TypeOf(execution.FreezeSlotContractRequest{}),
		[]string{"QueryGroup", "ScheduleRevision", "ScheduleSegmentStart", "EvaluationTime", "DuePlans"})
}

func assertScheduleFields(t *testing.T, contractType reflect.Type, want []string) {
	t.Helper()
	if got := fieldNames(contractType); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s fields = %v, want %v", contractType.Name(), got, want)
	}
}

func schedulePlan(strategyID string) execution.PlanIdentity {
	return execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: strategyID}
}
