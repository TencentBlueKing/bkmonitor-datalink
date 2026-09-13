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

// TestOutputContextRefsResolveByEvaluationTime: the context a Slot renders
// with is decided by its evaluation time alone. A Slot before the first
// revision keeps the base refs whenever it is resolved; a Slot at or after
// a revision's Since gets that revision; a later revision wins over an
// earlier one; and revisions must advance past the Segment start in order.
func TestOutputContextRefsResolveByEvaluationTime(t *testing.T) {
	end := execution.EvaluationTime(1000)
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "a"}
	base := []execution.OutputContextRef{{Plan: plan, Digest: "ctx-1"}}
	second := []execution.OutputContextRef{{Plan: plan, Digest: "ctx-2"}}
	third := []execution.OutputContextRef{{Plan: plan, Digest: "ctx-3"}}
	segment := execution.ScheduleSegmentFact{
		Publication: execution.SnapshotPublicationRef{SnapshotRevision: "rev", PublicationEpoch: 1},
		QueryGroup:  "qg", QueryRevision: "q", ScheduleRevision: "s", Start: 100, End: &end,
		ObjectDigest: "obj", OutputContextRefs: base,
		OutputContextRevisions: []execution.OutputContextRevision{{Since: 300, Refs: second}, {Since: 600, Refs: third}},
	}
	if err := segment.Validate(); err != nil {
		t.Fatal(err)
	}
	for at, want := range map[execution.EvaluationTime]execution.OutputContextDigest{100: "ctx-1", 299: "ctx-1", 300: "ctx-2", 599: "ctx-2", 600: "ctx-3", 999: "ctx-3"} {
		resolved := segment.At(at)
		if got := resolved.OutputContextRefFor(plan); got != want {
			t.Fatalf("at %d resolved %s, want %s", at, got, want)
		}
		if resolved.OutputContextRevisions != nil || resolved.ObjectDigest != "obj" || resolved.Start != 100 {
			t.Fatalf("At must only replace the refs: %+v", resolved)
		}
	}
	if !execution.SameOutputContextRefs(base, []execution.OutputContextRef{{Plan: plan, Digest: "ctx-1"}}) ||
		execution.SameOutputContextRefs(base, second) || execution.SameOutputContextRefs(base, nil) {
		t.Fatal("SameOutputContextRefs must compare by Plan and digest")
	}
	unordered := segment
	unordered.OutputContextRevisions = []execution.OutputContextRevision{{Since: 600, Refs: third}, {Since: 300, Refs: second}}
	if err := unordered.Validate(); err == nil {
		t.Fatal("revisions out of order must be refused")
	}
	atStart := segment
	atStart.OutputContextRevisions = []execution.OutputContextRevision{{Since: 100, Refs: second}}
	if err := atStart.Validate(); err == nil {
		t.Fatal("a revision at the Segment start must be refused: the base refs already cover it")
	}
	empty := segment
	empty.OutputContextRevisions = []execution.OutputContextRevision{{Since: 300}}
	if err := empty.Validate(); err == nil {
		t.Fatal("a revision without refs must be refused")
	}
}
