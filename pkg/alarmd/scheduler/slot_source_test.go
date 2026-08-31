// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package scheduler

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

func TestProductionSlotSourceColdStartFreezesFirstDueSlot(t *testing.T) {
	schedule := testSchedule()
	source, catalog := newTestProductionSlotSource(t, schedule, execution.ProgressLoadResult{Status: execution.ProgressMissing})

	slot, due, err := source.Next(context.Background(), schedule.QueryGroup)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if !due {
		t.Fatal("Next() due = false, want true")
	}
	if slot.Contract.Slot.EvaluationTime != 120 || slot.ExpectedNextSlot != 120 || slot.NextSlotAfterCompletion != 180 {
		t.Fatalf("slot times = %+v", slot)
	}
	if slot.Contract.SnapshotRevision != "snapshot-1" || slot.Contract.QueryRevision != "query-1" ||
		slot.Contract.ScheduleRevision != schedule.ScheduleRevision || slot.Contract.DuePlanSetDigest != "due-plan-set-1" {
		t.Fatalf("frozen contract = %+v", slot.Contract)
	}
	wantDue := []FrozenPlanScheduleRef{
		{Identity: planIdentity("1"), ScheduleRevision: "plan-schedule-1"},
		{Identity: planIdentity("2"), ScheduleRevision: "plan-schedule-2"},
	}
	if !reflect.DeepEqual(catalog.requests[0].DuePlans, wantDue) {
		t.Fatalf("due plans = %+v, want %+v", catalog.requests[0].DuePlans, wantDue)
	}
	if slot.Dispatch.Operation != execution.OperationNormal || slot.Dispatch.AssignmentGeneration != 3 ||
		slot.Dispatch.OwnerFence.OwnerEpoch != 7 {
		t.Fatalf("dispatch context = %+v", slot.Dispatch)
	}
}

func TestProductionSlotSourceUsesContinuousProgress(t *testing.T) {
	schedule := testSchedule()
	namespace := execution.ProgressNamespace{QueryGroup: schedule.QueryGroup, ScheduleRevision: schedule.ScheduleRevision}
	load := execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Namespace: namespace, NextSlot: 180, LastFullSlot: 120, LastCompletionKind: execution.CompletionFull,
	}}
	source, catalog := newTestProductionSlotSource(t, schedule, load)

	slot, due, err := source.Next(context.Background(), schedule.QueryGroup)
	if err != nil || !due {
		t.Fatalf("Next() due=%v error=%v", due, err)
	}
	if slot.ExpectedNextSlot != 180 || slot.NextSlotAfterCompletion != 240 {
		t.Fatalf("slot times = %+v", slot)
	}
	wantDue := []FrozenPlanScheduleRef{{Identity: planIdentity("1"), ScheduleRevision: "plan-schedule-1"}}
	if !reflect.DeepEqual(catalog.requests[0].DuePlans, wantDue) {
		t.Fatalf("due plans = %+v, want %+v", catalog.requests[0].DuePlans, wantDue)
	}
}

func TestProductionSlotSourceFreezesContentRevisionByEvaluationTimeAcrossCutover(t *testing.T) {
	schedule := testSchedule()
	freeze := func(request FreezeSlotContractRequest) execution.FrozenExecutionContractRef {
		snapshotRevision := execution.SnapshotRevision("snapshot-before-cutover")
		queryRevision := execution.QueryRevision("query-before-cutover")
		if request.EvaluationTime >= 180 {
			snapshotRevision = "snapshot-after-cutover"
			queryRevision = "query-after-cutover"
		}
		return execution.FrozenExecutionContractRef{
			Slot: execution.SlotIdentity{QueryGroup: request.QueryGroup, ScheduleRevision: request.ScheduleRevision,
				EvaluationTime: request.EvaluationTime},
			SnapshotRevision: snapshotRevision, QueryRevision: queryRevision,
			ScheduleRevision: request.ScheduleRevision, DuePlanSetDigest: "due-plan-set-after-cutover",
		}
	}
	tests := []struct {
		name         string
		load         execution.ProgressLoadResult
		wantSnapshot execution.SnapshotRevision
		wantQuery    execution.QueryRevision
	}{
		{name: "before cutover", load: execution.ProgressLoadResult{Status: execution.ProgressMissing},
			wantSnapshot: "snapshot-before-cutover", wantQuery: "query-before-cutover"},
		{name: "after cutover", load: foundProgress(schedule, 180, 120),
			wantSnapshot: "snapshot-after-cutover", wantQuery: "query-after-cutover"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := &fakeSlotCatalog{schedule: schedule, freeze: freeze}
			source := mustProductionSlotSource(t, schedule.QueryGroup, schedule.ScheduleRevision,
				&fakeAssignmentReader{records: []ownership.AssignmentRecord{testAssignment("worker-1", 3)}},
				&sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}}, catalog,
				&fakeProgressReader{result: test.load}, time.Unix(200, 0))

			slot, due, err := source.Next(context.Background(), schedule.QueryGroup)
			if err != nil || !due {
				t.Fatalf("Next() due=%v error=%v", due, err)
			}
			if slot.Contract.SnapshotRevision != test.wantSnapshot || slot.Contract.QueryRevision != test.wantQuery {
				t.Fatalf("contract content revisions = %+v", slot.Contract)
			}
		})
	}
}

func TestProductionSlotSourceReadsOnlyBoundScheduleLane(t *testing.T) {
	for _, revision := range []execution.ScheduleRevision{"schedule-1", "schedule-2"} {
		t.Run(string(revision), func(t *testing.T) {
			schedule := testSchedule()
			schedule.ScheduleRevision = revision
			catalog := &fakeSlotCatalog{schedule: schedule}
			source := mustProductionSlotSource(t, schedule.QueryGroup, revision,
				&fakeAssignmentReader{records: []ownership.AssignmentRecord{testAssignment("worker-1", 3)}},
				&sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}}, catalog,
				&fakeProgressReader{result: execution.ProgressLoadResult{Status: execution.ProgressMissing}}, time.Unix(200, 0))

			if _, due, err := source.Next(context.Background(), schedule.QueryGroup); err != nil || !due {
				t.Fatalf("Next() due=%v error=%v", due, err)
			}
			if catalog.readScheduleRevision != revision {
				t.Fatalf("ReadFrozenSchedule() revision = %q, want %q", catalog.readScheduleRevision, revision)
			}
			if catalog.requests[0].ScheduleRevision != revision {
				t.Fatalf("FreezeSlotContract() revision = %q, want %q", catalog.requests[0].ScheduleRevision, revision)
			}
		})
	}
}

func TestProductionSlotSourceRestartWithSameProgressFreezesSameContract(t *testing.T) {
	schedule := testSchedule()
	namespace := execution.ProgressNamespace{QueryGroup: schedule.QueryGroup, ScheduleRevision: schedule.ScheduleRevision}
	load := execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Namespace: namespace, NextSlot: 180, LastFullSlot: 120, LastCompletionKind: execution.CompletionFull,
	}}

	first, _ := newTestProductionSlotSource(t, schedule, load)
	second, _ := newTestProductionSlotSource(t, schedule, load)
	firstSlot, firstDue, firstErr := first.Next(context.Background(), schedule.QueryGroup)
	secondSlot, secondDue, secondErr := second.Next(context.Background(), schedule.QueryGroup)
	if firstErr != nil || secondErr != nil || !firstDue || !secondDue {
		t.Fatalf("restart results first=(%v,%v) second=(%v,%v)", firstDue, firstErr, secondDue, secondErr)
	}
	if !reflect.DeepEqual(firstSlot.Contract, secondSlot.Contract) || firstSlot.ExpectedNextSlot != secondSlot.ExpectedNextSlot {
		t.Fatalf("contracts drifted across restart: first=%+v second=%+v", firstSlot, secondSlot)
	}
}

func TestProductionSlotSourceReturnsNotDueBeforeEvaluationTime(t *testing.T) {
	schedule := testSchedule()
	source, catalog := newTestProductionSlotSourceAt(t, schedule, execution.ProgressLoadResult{Status: execution.ProgressMissing}, time.Unix(119, 0))

	_, due, err := source.Next(context.Background(), schedule.QueryGroup)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if due {
		t.Fatal("Next() due = true before EvaluationTime")
	}
	if len(catalog.requests) != 0 {
		t.Fatalf("FreezeContract calls = %d, want 0", len(catalog.requests))
	}
}

func TestProductionSlotSourceRejectsProgressOutsideSchedule(t *testing.T) {
	schedule := testSchedule()
	namespace := execution.ProgressNamespace{QueryGroup: schedule.QueryGroup, ScheduleRevision: schedule.ScheduleRevision}
	load := execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Namespace: namespace, NextSlot: 181, LastFullSlot: 120, LastCompletionKind: execution.CompletionFull,
	}}
	source, _ := newTestProductionSlotSource(t, schedule, load)

	if _, _, err := source.Next(context.Background(), schedule.QueryGroup); !errors.Is(err, ErrProgressOffSchedule) {
		t.Fatalf("Next() error = %v, want ErrProgressOffSchedule", err)
	}
}

func TestProductionSlotSourceRejectsMissingDueFacts(t *testing.T) {
	schedule := testSchedule()
	schedule.Plans = nil
	source, _ := newTestProductionSlotSource(t, schedule, execution.ProgressLoadResult{Status: execution.ProgressMissing})

	if _, _, err := source.Next(context.Background(), schedule.QueryGroup); !errors.Is(err, ErrScheduleFactsInvalid) {
		t.Fatalf("Next() error = %v, want ErrScheduleFactsInvalid", err)
	}
}

func TestProductionSlotSourceRejectsNonCurrentAssignmentAndLostLease(t *testing.T) {
	tests := []struct {
		name       string
		assignment ownership.AssignmentRecord
		sessionErr error
		want       error
	}{
		{name: "not desired", assignment: testAssignment("worker-2", 3), want: ownership.ErrNotDesired},
		{name: "lost lease", assignment: testAssignment("worker-1", 3), sessionErr: ownership.ErrStaleFence, want: ownership.ErrStaleFence},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schedule := testSchedule()
			assignments := &fakeAssignmentReader{records: []ownership.AssignmentRecord{test.assignment}}
			session := &sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}, err: test.sessionErr}
			catalog := &fakeSlotCatalog{schedule: schedule}
			source := mustProductionSlotSource(t, schedule.QueryGroup, schedule.ScheduleRevision, assignments, session, catalog,
				&fakeProgressReader{result: execution.ProgressLoadResult{Status: execution.ProgressMissing}}, time.Unix(200, 0))

			if _, _, err := source.Next(context.Background(), schedule.QueryGroup); !errors.Is(err, test.want) {
				t.Fatalf("Next() error = %v, want %v", err, test.want)
			}
			if catalog.reads != 0 {
				t.Fatalf("catalog reads = %d, want 0", catalog.reads)
			}
		})
	}
}

func TestProductionSlotSourceRejectsOwnershipChangeWhileFreezing(t *testing.T) {
	tests := []struct {
		name        string
		assignments []ownership.AssignmentRecord
		fences      []execution.OwnerFence
	}{
		{name: "assignment generation", assignments: []ownership.AssignmentRecord{testAssignment("worker-1", 3), testAssignment("worker-1", 4)}, fences: []execution.OwnerFence{testFence(7), testFence(7)}},
		{name: "owner epoch", assignments: []ownership.AssignmentRecord{testAssignment("worker-1", 3), testAssignment("worker-1", 3)}, fences: []execution.OwnerFence{testFence(7), testFence(8)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schedule := testSchedule()
			source := mustProductionSlotSource(t, schedule.QueryGroup, schedule.ScheduleRevision, &fakeAssignmentReader{records: test.assignments},
				&sequenceOwnerSession{fences: test.fences}, &fakeSlotCatalog{schedule: schedule},
				&fakeProgressReader{result: execution.ProgressLoadResult{Status: execution.ProgressMissing}}, time.Unix(200, 0))

			if _, _, err := source.Next(context.Background(), schedule.QueryGroup); !errors.Is(err, ErrSlotOwnershipChanged) {
				t.Fatalf("Next() error = %v, want ErrSlotOwnershipChanged", err)
			}
		})
	}
}

func TestProductionSlotSourceRejectsCatalogContractDrift(t *testing.T) {
	schedule := testSchedule()
	valid := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: schedule.QueryGroup, ScheduleRevision: schedule.ScheduleRevision, EvaluationTime: 120},
		SnapshotRevision: "snapshot-1",
		QueryRevision:    "query-1",
		ScheduleRevision: schedule.ScheduleRevision,
		DuePlanSetDigest: "due-plan-set-1",
	}
	tests := []struct {
		name   string
		mutate func(*execution.FrozenExecutionContractRef)
	}{
		{name: "schedule revision", mutate: func(ref *execution.FrozenExecutionContractRef) { ref.ScheduleRevision = "schedule-changed" }},
		{name: "missing due Plan digest", mutate: func(ref *execution.FrozenExecutionContractRef) { ref.DuePlanSetDigest = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contractRef := valid
			test.mutate(&contractRef)
			catalog := &fakeSlotCatalog{schedule: schedule, contract: contractRef}
			source := mustProductionSlotSource(t, schedule.QueryGroup, schedule.ScheduleRevision,
				&fakeAssignmentReader{records: []ownership.AssignmentRecord{testAssignment("worker-1", 3)}},
				&sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}}, catalog,
				&fakeProgressReader{result: execution.ProgressLoadResult{Status: execution.ProgressMissing}}, time.Unix(200, 0))

			if _, _, err := source.Next(context.Background(), schedule.QueryGroup); !errors.Is(err, ErrSlotContractDrift) {
				t.Fatalf("Next() error = %v, want ErrSlotContractDrift", err)
			}
		})
	}
}

func testSchedule() FrozenQueryGroupSchedule {
	return FrozenQueryGroupSchedule{
		QueryGroup: "query-group-1", ScheduleRevision: "schedule-1", FirstEvaluationTime: 120,
		Plans: []FrozenPlanSchedule{
			{Identity: planIdentity("2"), ScheduleRevision: "plan-schedule-2", IntervalSeconds: 120, Alignment: 0},
			{Identity: planIdentity("1"), ScheduleRevision: "plan-schedule-1", IntervalSeconds: 60, Alignment: 0},
		},
	}
}

func foundProgress(schedule FrozenQueryGroupSchedule, nextSlot, lastFullSlot execution.EvaluationTime) execution.ProgressLoadResult {
	return execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Namespace: execution.ProgressNamespace{QueryGroup: schedule.QueryGroup, ScheduleRevision: schedule.ScheduleRevision},
		NextSlot:  nextSlot, LastFullSlot: lastFullSlot, LastCompletionKind: execution.CompletionFull,
	}}
}

func planIdentity(strategyID string) execution.PlanIdentity {
	return execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: strategyID}
}

func testAssignment(workerID string, generation uint64) ownership.AssignmentRecord {
	return ownership.AssignmentRecord{QueryGroup: "query-group-1", DesiredWorkerID: workerID,
		AssignmentGeneration: generation, RecordRevision: generation, ControlEpoch: 2,
		PlacementReason: ownership.PlacementRendezvous, AssignedAt: time.Unix(50, 0)}
}

func testFence(epoch uint64) execution.OwnerFence {
	return execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: epoch, LeaseToken: "lease-token"}
}

func newTestProductionSlotSource(
	t *testing.T,
	schedule FrozenQueryGroupSchedule,
	load execution.ProgressLoadResult,
) (*ProductionSlotSource, *fakeSlotCatalog) {
	return newTestProductionSlotSourceAt(t, schedule, load, time.Unix(200, 0))
}

func newTestProductionSlotSourceAt(
	t *testing.T,
	schedule FrozenQueryGroupSchedule,
	load execution.ProgressLoadResult,
	at time.Time,
) (*ProductionSlotSource, *fakeSlotCatalog) {
	catalog := &fakeSlotCatalog{schedule: schedule}
	source := mustProductionSlotSource(t, schedule.QueryGroup, schedule.ScheduleRevision,
		&fakeAssignmentReader{records: []ownership.AssignmentRecord{testAssignment("worker-1", 3)}},
		&sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}}, catalog,
		&fakeProgressReader{result: load}, at)
	return source, catalog
}

func mustProductionSlotSource(
	t *testing.T,
	queryGroup execution.QueryGroupIdentity,
	scheduleRevision execution.ScheduleRevision,
	assignments AssignmentReader,
	session OwnerSession,
	catalog SlotCatalogReader,
	progress ScheduleProgressReader,
	at time.Time,
) *ProductionSlotSource {
	t.Helper()
	source, err := NewProductionSlotSource(queryGroup, scheduleRevision, "worker-1", assignments, session, catalog, progress, func() time.Time { return at })
	if err != nil {
		t.Fatalf("NewProductionSlotSource() error = %v", err)
	}
	return source
}

type fakeAssignmentReader struct {
	records []ownership.AssignmentRecord
	reads   int
}

func (reader *fakeAssignmentReader) ReadAssignment(context.Context, execution.QueryGroupIdentity) (ownership.AssignmentRecord, error) {
	index := reader.reads
	if index >= len(reader.records) {
		index = len(reader.records) - 1
	}
	reader.reads++
	return reader.records[index], nil
}

type sequenceOwnerSession struct {
	fences []execution.OwnerFence
	reads  int
	err    error
}

func (session *sequenceOwnerSession) ValidateCurrent(context.Context, time.Time) (execution.OwnerFence, error) {
	if session.err != nil {
		return execution.OwnerFence{}, session.err
	}
	index := session.reads
	if index >= len(session.fences) {
		index = len(session.fences) - 1
	}
	session.reads++
	return session.fences[index], nil
}

type fakeSlotCatalog struct {
	schedule             FrozenQueryGroupSchedule
	contract             execution.FrozenExecutionContractRef
	freeze               func(FreezeSlotContractRequest) execution.FrozenExecutionContractRef
	requests             []FreezeSlotContractRequest
	reads                int
	readScheduleRevision execution.ScheduleRevision
}

func (catalog *fakeSlotCatalog) ReadFrozenSchedule(
	_ context.Context,
	_ execution.QueryGroupIdentity,
	scheduleRevision execution.ScheduleRevision,
) (FrozenQueryGroupSchedule, error) {
	catalog.reads++
	catalog.readScheduleRevision = scheduleRevision
	return catalog.schedule, nil
}

func (catalog *fakeSlotCatalog) FreezeSlotContract(_ context.Context, request FreezeSlotContractRequest) (execution.FrozenExecutionContractRef, error) {
	catalog.requests = append(catalog.requests, request)
	if catalog.freeze != nil {
		return catalog.freeze(request), nil
	}
	if catalog.contract.Slot.QueryGroup != "" {
		return catalog.contract, nil
	}
	return execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: request.QueryGroup, ScheduleRevision: request.ScheduleRevision, EvaluationTime: request.EvaluationTime},
		SnapshotRevision: "snapshot-1", QueryRevision: "query-1",
		ScheduleRevision: request.ScheduleRevision, DuePlanSetDigest: "due-plan-set-1",
	}, nil
}

type fakeProgressReader struct {
	result execution.ProgressLoadResult
}

func (reader *fakeProgressReader) LoadProgress(context.Context, execution.ProgressNamespace) (execution.ProgressLoadResult, error) {
	return reader.result, nil
}
