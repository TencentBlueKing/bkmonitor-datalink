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

func TestSlotIdentityContainsOnlyQueryGroupAndEvaluationTime(t *testing.T) {
	fields := fieldNames(reflect.TypeOf(execution.SlotIdentity{}))
	want := []string{"QueryGroup", "EvaluationTime"}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("SlotIdentity fields = %v, want %v", fields, want)
	}
}

func TestFrozenContractKeepsScheduleAsProvenance(t *testing.T) {
	fields := fieldNames(reflect.TypeOf(execution.FrozenExecutionContractRef{}))
	want := []string{"Slot", "SnapshotRevision", "QueryRevision", "ScheduleRevision", "ScheduleSegmentStart", "DuePlanSetDigest"}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("FrozenExecutionContractRef fields = %v, want %v", fields, want)
	}
}

func TestScheduleProgressUsesQueryGroupIdentity(t *testing.T) {
	fields := fieldNames(reflect.TypeOf(execution.ScheduleProgress{}))
	want := []string{"Identity", "NextSlot", "LastFullSlot", "LastCompletionKind", "CurrentOrRecentGap", "UnfinishedSlot", "UnfinishedRange"}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("ScheduleProgress fields = %v, want %v", fields, want)
	}
}

func TestProgressContractsDoNotFreezeNextSlotAfterCompletion(t *testing.T) {
	if got, want := fieldNames(reflect.TypeOf(execution.SlotExecutionRequest{})),
		[]string{"ShortPeriodCohort", "Contract", "DuePlanTargets", "EarliestQueryDeadlineUnixMilli", "RecoveryUntilUnixMilli", "KeepUntilUnixMilli", "ReplayExpired", "Operation", "AttemptNo", "OwnerFence", "ExpectedNextSlot", "ExpiredRange"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SlotExecutionRequest fields = %v, want %v", got, want)
	}
	if got, want := fieldNames(reflect.TypeOf(execution.ProgressCommitRequest{})),
		[]string{"Identity", "OwnerFence", "ExpectedNextSlot", "Completion", "Projection"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ProgressCommitRequest fields = %v, want %v", got, want)
	}
}

func TestSlotExecutionRequestCarriesValidatedNonIdentityFrozenExecutionFacts(t *testing.T) {
	contractRef := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: "query-group", EvaluationTime: 120},
		SnapshotRevision: "snapshot-v1", QueryRevision: "query-v1", ScheduleRevision: "schedule-v1",
		ScheduleSegmentStart: 60, DuePlanSetDigest: "due-v1",
	}
	request := execution.SlotExecutionRequest{
		Contract: contractRef,
		DuePlanTargets: execution.FrozenDuePlanTargets{
			DuePlanSetDigest: contractRef.DuePlanSetDigest,
			Plans:            []execution.PlanIdentity{{TenantID: "tenant-a", BusinessID: "2", StrategyID: "1001"}},
		},
		EarliestQueryDeadlineUnixMilli: 175_000,
		RecoveryUntilUnixMilli:         775_000,
		KeepUntilUnixMilli:             851_000,
		Operation:                      execution.OperationNormal, AttemptNo: 1,
		OwnerFence:       execution.OwnerFence{QueryGroup: "query-group", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "lease-1"},
		ExpectedNextSlot: 120,
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*execution.SlotExecutionRequest)
	}{
		{name: "missing targets", mutate: func(request *execution.SlotExecutionRequest) { request.DuePlanTargets.Plans = nil }},
		{name: "digest mismatch", mutate: func(request *execution.SlotExecutionRequest) { request.DuePlanTargets.DuePlanSetDigest = "other" }},
		{name: "duplicate target", mutate: func(request *execution.SlotExecutionRequest) {
			request.DuePlanTargets.Plans = append(request.DuePlanTargets.Plans, request.DuePlanTargets.Plans[0])
		}},
		{name: "deadline not after Slot", mutate: func(request *execution.SlotExecutionRequest) {
			request.EarliestQueryDeadlineUnixMilli = int64(request.Contract.Slot.EvaluationTime) * 1000
		}},
		{name: "replay expired with recovery dispatch", mutate: func(request *execution.SlotExecutionRequest) {
			request.ReplayExpired = true
			request.Operation = execution.OperationReplay
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := request
			candidate.DuePlanTargets = request.DuePlanTargets.Clone()
			test.mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("Validate() error=nil")
			}
		})
	}
}

func TestQueryAttemptFactsStayOutsideBusinessIdentity(t *testing.T) {
	if got, want := fieldNames(reflect.TypeOf(execution.QueryExecutionRequest{})),
		[]string{"Contract", "Operation", "AttemptNo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("QueryExecutionRequest fields = %v, want %v", got, want)
	}
	if got, want := fieldNames(reflect.TypeOf(execution.SlotIdentity{})),
		[]string{"QueryGroup", "EvaluationTime"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SlotIdentity fields = %v, want %v", got, want)
	}
}

func TestInitialScheduleActivationCreatesFirstOpenSegment(t *testing.T) {
	schedule := baselineSchedule(t, "query-group", 60, 60, nil, "snapshot-first", 1)
	fact := execution.InitialScheduleActivationFact{Segment: schedule.Segment}
	if err := fact.Validate(schedule); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	closedAt := execution.EvaluationTime(180)
	fact.Segment.End = &closedAt
	if err := fact.Validate(schedule); err == nil {
		t.Fatal("Validate() accepted a closed initial Segment")
	}
}

func TestScheduleCutoverCreatesAdjacentHalfOpenSegments(t *testing.T) {
	boundary := execution.EvaluationTime(75)
	oldSchedule := baselineSchedule(t, "query-group", 60, 30, &boundary, "snapshot-old", 7)
	newSchedule := baselineSchedule(t, "query-group", 90, boundary, nil, "snapshot-new", 8)
	fact := execution.ScheduleCutoverFact{OldSegment: oldSchedule.Segment, NewSegment: newSchedule.Segment}
	if err := fact.Validate(oldSchedule, newSchedule); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got, ok := newSchedule.FirstSlot(); !ok || got != 90 {
		t.Fatalf("new FirstSlot() = %d, %t; want 90, true", got, ok)
	}
	if due := oldSchedule.DuePlanRefs(boundary); len(due) != 0 {
		t.Fatalf("old Segment owns boundary due Plans: %+v", due)
	}

	overlapped := fact
	overlapped.NewSegment.Start = boundary - 1
	if err := overlapped.Validate(oldSchedule, newSchedule); err == nil {
		t.Fatal("Validate() accepted overlapping Segments")
	}
}

func TestScheduleCutoverAllowsOldSegmentReplacedBeforeItsFirstSlot(t *testing.T) {
	boundary := execution.EvaluationTime(90)
	oldSchedule := baselineSchedule(t, "query-group", 60, 83, &boundary, "snapshot-old", 7)
	newSchedule := baselineSchedule(t, "query-group", 60, boundary, nil, "snapshot-new", 8)
	fact := execution.ScheduleCutoverFact{OldSegment: oldSchedule.Segment, NewSegment: newSchedule.Segment}

	if err := fact.Validate(oldSchedule, newSchedule); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if first, ok := oldSchedule.FirstSlot(); ok {
		t.Fatalf("old FirstSlot() = (%d, %t), want no owned Slot", first, ok)
	}
	if due := oldSchedule.DuePlanRefs(boundary); len(due) != 0 {
		t.Fatalf("old Segment owns boundary due Plans: %+v", due)
	}
	if due := oldSchedule.DuePlanRefs(120); len(due) != 0 {
		t.Fatalf("old Segment owns post-boundary due Plans: %+v", due)
	}
	if first, ok := newSchedule.FirstSlot(); !ok || first != 120 {
		t.Fatalf("new FirstSlot() = (%d, %t), want (120, true)", first, ok)
	}
	if due := newSchedule.DuePlanRefs(boundary); len(due) != 0 {
		t.Fatalf("new Segment fabricated a non-grid boundary Slot: %+v", due)
	}
}

func TestFrozenSlotRejectsTamperedDeadlineAfterDigestRecomputed(t *testing.T) {
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
		QueryGroup: "query-group", ScheduleRevision: scheduleRevision,
		ScheduleSegmentStart: evaluationTime - 60, EvaluationTime: evaluationTime,
		DuePlans: []execution.FrozenPlanScheduleRef{{Identity: plans[0].Identity, ScheduleRevision: plans[0].ScheduleRevision}},
	}
	digest, err := execution.DeriveDuePlanSetDigest(plans, requirements)
	if err != nil {
		t.Fatalf("DeriveDuePlanSetDigest() error = %v", err)
	}
	fact := execution.FrozenSlotContractFact{
		Contract: execution.FrozenExecutionContractRef{
			Slot:             execution.SlotIdentity{QueryGroup: request.QueryGroup, EvaluationTime: evaluationTime},
			SnapshotRevision: "snapshot-v1", QueryRevision: "query-v1", ScheduleRevision: scheduleRevision,
			ScheduleSegmentStart: request.ScheduleSegmentStart, DuePlanSetDigest: digest,
		},
		DuePlans: plans, Requirements: requirements,
	}
	if err := fact.Validate(request); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	fact.DuePlans[0].CompletionDeadlineUnixMilli++
	fact.Contract.DuePlanSetDigest, err = fact.DeriveDuePlanSetDigest()
	if err != nil {
		t.Fatalf("DeriveDuePlanSetDigest(tampered) error = %v", err)
	}
	if err := fact.Validate(request); err == nil {
		t.Fatal("Validate() accepted a tampered deadline after digest recomputation")
	}
}

func baselineSchedule(
	t *testing.T,
	queryGroup execution.QueryGroupIdentity,
	interval int64,
	start execution.EvaluationTime,
	end *execution.EvaluationTime,
	snapshot execution.SnapshotRevision,
	epoch execution.PublicationEpoch,
) execution.FrozenQueryGroupSchedule {
	t.Helper()
	planSpec := execution.ScheduleSpec{EvaluationIntervalSeconds: interval, Alignment: 0, Timezone: "UTC"}
	plan := execution.FrozenPlanSchedule{
		Identity:         execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1"},
		ScheduleRevision: mustBaselinePlanRevision(t, planSpec), Spec: planSpec,
	}
	scheduleRevision, err := execution.DeriveQueryGroupScheduleRevision([]execution.FrozenPlanSchedule{plan})
	if err != nil {
		t.Fatalf("DeriveQueryGroupScheduleRevision() error = %v", err)
	}
	return execution.FrozenQueryGroupSchedule{
		Segment: execution.ScheduleSegmentFact{
			Publication: execution.SnapshotPublicationRef{SnapshotRevision: snapshot, PublicationEpoch: epoch},
			QueryGroup:  queryGroup, QueryRevision: "query-v1", ScheduleRevision: scheduleRevision, Start: start, End: end,
		},
		Plans: []execution.FrozenPlanSchedule{plan},
	}
}

func mustBaselinePlanRevision(t *testing.T, spec execution.ScheduleSpec) execution.PlanScheduleRevision {
	t.Helper()
	revision, err := execution.DerivePlanScheduleRevision(spec)
	if err != nil {
		t.Fatalf("DerivePlanScheduleRevision() error = %v", err)
	}
	return revision
}

func fieldNames(contractType reflect.Type) []string {
	fields := make([]string, contractType.NumField())
	for index := range fields {
		fields[index] = contractType.Field(index).Name
	}
	return fields
}
