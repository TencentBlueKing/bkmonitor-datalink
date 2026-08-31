// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package execution_test

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestScheduleSpecRevisionIsStableAndComplete(t *testing.T) {
	base := execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Alignment: 0, Timezone: "UTC"}
	baseRevision, err := execution.DerivePlanScheduleRevision(base)
	if err != nil {
		t.Fatalf("DerivePlanScheduleRevision() error = %v", err)
	}
	if baseRevision == "" {
		t.Fatal("DerivePlanScheduleRevision() returned an empty revision")
	}
	if again, err := execution.DerivePlanScheduleRevision(base); err != nil || again != baseRevision {
		t.Fatalf("stable revision = %q, %v; want %q", again, err, baseRevision)
	}

	for _, test := range []struct {
		name string
		spec execution.ScheduleSpec
	}{
		{name: "evaluation interval", spec: execution.ScheduleSpec{EvaluationIntervalSeconds: 120, Alignment: 0, Timezone: "UTC"}},
		{name: "alignment", spec: execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Alignment: 30, Timezone: "UTC"}},
		{name: "timezone", spec: execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Alignment: 0, Timezone: "Asia/Shanghai"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			revision, deriveErr := execution.DerivePlanScheduleRevision(test.spec)
			if deriveErr != nil {
				t.Fatalf("DerivePlanScheduleRevision() error = %v", deriveErr)
			}
			if revision == baseRevision {
				t.Fatalf("revision = %q after %s changed", revision, test.name)
			}
		})
	}

}

func TestScheduleSpecRejectsInvalidAlignmentAndTimezone(t *testing.T) {
	for _, spec := range []execution.ScheduleSpec{
		{EvaluationIntervalSeconds: 0, Alignment: 0, Timezone: "UTC"},
		{EvaluationIntervalSeconds: 60, Alignment: -1, Timezone: "UTC"},
		{EvaluationIntervalSeconds: 60, Alignment: 0, Timezone: ""},
		{EvaluationIntervalSeconds: 60, Alignment: 0, Timezone: "not/a-timezone"},
	} {
		if _, err := execution.DerivePlanScheduleRevision(spec); err == nil {
			t.Fatalf("DerivePlanScheduleRevision(%+v) error = nil", spec)
		}
	}
}

func TestQueryGroupScheduleRevisionIgnoresPlanOrder(t *testing.T) {
	first := frozenPlanSchedule(t, "1", execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Alignment: 0, Timezone: "UTC"})
	second := frozenPlanSchedule(t, "2", execution.ScheduleSpec{EvaluationIntervalSeconds: 120, Alignment: 0, Timezone: "UTC"})

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

	second.Spec.EvaluationIntervalSeconds = 180
	second.ScheduleRevision = mustPlanScheduleRevision(t, second.Spec)
	changed, err := execution.DeriveQueryGroupScheduleRevision([]execution.FrozenPlanSchedule{first, second})
	if err != nil {
		t.Fatalf("DeriveQueryGroupScheduleRevision(changed) error = %v", err)
	}
	if changed == forward {
		t.Fatalf("QG schedule revision = %q after one Plan schedule changed", changed)
	}
}

func TestScheduleCutoverFactValidatesCrossLaneAndContentCutover(t *testing.T) {
	oldSchedule := frozenQueryGroupSchedule(t, "query-group-old", 60, 60)
	newSchedule := frozenQueryGroupSchedule(t, "query-group-new", 90, 90)
	fact := execution.ScheduleCutoverFact{
		OldPublication:        execution.SnapshotPublicationRef{SnapshotRevision: "snapshot-old", PublicationEpoch: 7},
		NewPublication:        execution.SnapshotPublicationRef{SnapshotRevision: "snapshot-new", PublicationEpoch: 8},
		OldLane:               oldSchedule.Lane,
		NewLane:               newSchedule.Lane,
		FirstEvaluationTime:   newSchedule.FirstEvaluationTime,
		CutoverEvaluationTime: 180,
	}
	if err := fact.Validate(oldSchedule, newSchedule); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	sameLane := oldSchedule
	sameLaneFact := fact
	sameLaneFact.NewLane = sameLane.Lane
	sameLaneFact.FirstEvaluationTime = sameLane.FirstEvaluationTime
	sameLaneFact.CutoverEvaluationTime = 180
	if err := sameLaneFact.Validate(oldSchedule, sameLane); err != nil {
		t.Fatalf("same-lane content cutover Validate() error = %v", err)
	}
}

func TestScheduleCutoverFactRejectsNonPositiveAndOffGridTimes(t *testing.T) {
	oldSchedule := frozenQueryGroupSchedule(t, "query-group-old", 60, 60)
	newSchedule := frozenQueryGroupSchedule(t, "query-group-new", 90, 90)
	valid := execution.ScheduleCutoverFact{
		OldPublication:        execution.SnapshotPublicationRef{SnapshotRevision: "snapshot-old", PublicationEpoch: 7},
		NewPublication:        execution.SnapshotPublicationRef{SnapshotRevision: "snapshot-new", PublicationEpoch: 8},
		OldLane:               oldSchedule.Lane,
		NewLane:               newSchedule.Lane,
		FirstEvaluationTime:   90,
		CutoverEvaluationTime: 180,
	}
	for _, test := range []struct {
		name   string
		mutate func(*execution.ScheduleCutoverFact)
	}{
		{name: "zero first evaluation time", mutate: func(fact *execution.ScheduleCutoverFact) { fact.FirstEvaluationTime = 0 }},
		{name: "zero cutover evaluation time", mutate: func(fact *execution.ScheduleCutoverFact) { fact.CutoverEvaluationTime = 0 }},
		{name: "cutover before first", mutate: func(fact *execution.ScheduleCutoverFact) { fact.CutoverEvaluationTime = 80 }},
		{name: "first off grid", mutate: func(fact *execution.ScheduleCutoverFact) { fact.FirstEvaluationTime = 91 }},
		{name: "cutover off grid", mutate: func(fact *execution.ScheduleCutoverFact) { fact.CutoverEvaluationTime = 181 }},
		{name: "publication epoch did not advance", mutate: func(fact *execution.ScheduleCutoverFact) { fact.NewPublication.PublicationEpoch = 7 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := candidate.Validate(oldSchedule, newSchedule); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}

	offGridSchedule := newSchedule
	offGridSchedule.FirstEvaluationTime = 91
	offGridFact := valid
	offGridFact.FirstEvaluationTime = 91
	if err := offGridFact.Validate(oldSchedule, offGridSchedule); err == nil {
		t.Fatal("Validate() accepted an off-grid FirstEvaluationTime persisted by both schedule and cutover fact")
	}
}

func TestFrozenSlotContractFactBindsExactDueRefsAndRecomputesDigest(t *testing.T) {
	plans, requirements := baseDuePlanAndRequirements()
	lane := execution.ScheduleLaneIdentity{QueryGroup: "query-group", ScheduleRevision: "schedule-v1"}
	request := execution.FreezeSlotContractRequest{
		Lane: lane, EvaluationTime: 1_788_000_000,
		DuePlans: []execution.FrozenPlanScheduleRef{{Identity: plans[0].Identity, ScheduleRevision: plans[0].ScheduleRevision}},
	}
	digest, err := execution.DeriveDuePlanSetDigest(plans, requirements)
	if err != nil {
		t.Fatalf("DeriveDuePlanSetDigest() error = %v", err)
	}
	fact := execution.FrozenSlotContractFact{
		Contract: execution.FrozenExecutionContractRef{
			Slot:             execution.SlotIdentity{QueryGroup: lane.QueryGroup, ScheduleRevision: lane.ScheduleRevision, EvaluationTime: request.EvaluationTime},
			SnapshotRevision: "snapshot-v1", QueryRevision: "query-v1", ScheduleRevision: lane.ScheduleRevision, DuePlanSetDigest: digest,
		},
		DuePlans: plans, Requirements: requirements,
	}
	if err := fact.Validate(request); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	recomputed, err := fact.DeriveDuePlanSetDigest()
	if err != nil {
		t.Fatalf("DeriveDuePlanSetDigest() error = %v", err)
	}
	if recomputed != digest {
		t.Fatalf("recomputed digest = %q, want %q", recomputed, digest)
	}

	drifted := request
	drifted.DuePlans = []execution.FrozenPlanScheduleRef{{Identity: planIdentityForSchedule("other"), ScheduleRevision: plans[0].ScheduleRevision}}
	if err := fact.Validate(drifted); err == nil {
		t.Fatal("Validate() accepted a different exact due Plan set")
	}

	tampered := fact
	tampered.Contract.DuePlanSetDigest = "tampered"
	if err := tampered.Validate(request); err == nil {
		t.Fatal("Validate() accepted a digest that cannot be independently recomputed")
	}
}

func TestScheduleLaneIdentitySeparatesRevisions(t *testing.T) {
	first := execution.ScheduleLaneIdentity{QueryGroup: "query-group", ScheduleRevision: "schedule-v1"}
	second := execution.ScheduleLaneIdentity{QueryGroup: "query-group", ScheduleRevision: "schedule-v2"}
	if err := first.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if first == second {
		t.Fatal("two schedule revisions collapsed into one lane")
	}
	if reflect.DeepEqual(first.ProgressNamespace(), second.ProgressNamespace()) {
		t.Fatal("two schedule lanes share one Progress namespace")
	}
}

func TestScheduleContractsExcludeRuntimeSelectorsAndOwnership(t *testing.T) {
	assertFieldNames(t, reflect.TypeOf(execution.FreezeSlotContractRequest{}),
		[]string{"Lane", "EvaluationTime", "DuePlans"})
	assertFieldNames(t, reflect.TypeOf(execution.ScheduleCutoverFact{}),
		[]string{"OldPublication", "NewPublication", "OldLane", "NewLane", "FirstEvaluationTime", "CutoverEvaluationTime"})
}

func assertFieldNames(t *testing.T, contractType reflect.Type, want []string) {
	t.Helper()
	got := make([]string, contractType.NumField())
	for index := range got {
		got[index] = contractType.Field(index).Name
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s fields = %v, want %v", contractType.Name(), got, want)
	}
}

func frozenPlanSchedule(t *testing.T, strategyID string, spec execution.ScheduleSpec) execution.FrozenPlanSchedule {
	t.Helper()
	return execution.FrozenPlanSchedule{
		Identity: planIdentityForSchedule(strategyID), Spec: spec, ScheduleRevision: mustPlanScheduleRevision(t, spec),
	}
}

func frozenQueryGroupSchedule(t *testing.T, queryGroup string, interval int64, first execution.EvaluationTime) execution.FrozenQueryGroupSchedule {
	t.Helper()
	plan := frozenPlanSchedule(t, "1", execution.ScheduleSpec{EvaluationIntervalSeconds: interval, Alignment: 0, Timezone: "UTC"})
	revision, err := execution.DeriveQueryGroupScheduleRevision([]execution.FrozenPlanSchedule{plan})
	if err != nil {
		t.Fatalf("DeriveQueryGroupScheduleRevision() error = %v", err)
	}
	return execution.FrozenQueryGroupSchedule{
		Lane:                execution.ScheduleLaneIdentity{QueryGroup: execution.QueryGroupIdentity(queryGroup), ScheduleRevision: revision},
		FirstEvaluationTime: first, Plans: []execution.FrozenPlanSchedule{plan},
	}
}

func mustPlanScheduleRevision(t *testing.T, spec execution.ScheduleSpec) execution.PlanScheduleRevision {
	t.Helper()
	revision, err := execution.DerivePlanScheduleRevision(spec)
	if err != nil {
		t.Fatalf("DerivePlanScheduleRevision() error = %v", err)
	}
	return revision
}

func planIdentityForSchedule(strategyID string) execution.PlanIdentity {
	return execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: strategyID}
}
