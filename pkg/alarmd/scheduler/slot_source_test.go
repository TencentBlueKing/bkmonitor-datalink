// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestProductionSlotSourceColdStartUsesFirstSegment(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(200, 0))

	slot, due, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("Next() due=%v error=%v", due, err)
	}
	if slot.Contract.Slot != (execution.SlotIdentity{QueryGroup: "query-group-1", EvaluationTime: 60}) {
		t.Fatalf("SlotIdentity = %+v", slot.Contract.Slot)
	}
	if slot.Contract.ScheduleRevision != schedule.Segment.ScheduleRevision ||
		slot.Contract.ScheduleSegmentStart != schedule.Segment.Start {
		t.Fatalf("frozen schedule provenance = %+v", slot.Contract)
	}
	if catalog.initialReads != 1 || len(catalog.readTimes) != 0 {
		t.Fatalf("catalog reads initial=%d by-time=%v", catalog.initialReads, catalog.readTimes)
	}
	if got := catalog.requests[0]; got.QueryGroup != "query-group-1" || got.ScheduleSegmentStart != 60 || got.EvaluationTime != 60 {
		t.Fatalf("FreezeSlotContract request = %+v", got)
	}
	if len(slot.DuePlanTargets.Plans) != 1 || slot.DuePlanTargets.Plans[0] != schedule.Plans[0].Identity ||
		slot.DuePlanTargets.DuePlanSetDigest != slot.Contract.DuePlanSetDigest ||
		slot.EarliestQueryDeadlineUnixMilli <= int64(slot.Contract.Slot.EvaluationTime)*1000 {
		t.Fatalf("frozen execution facts = targets=%+v deadline=%d", slot.DuePlanTargets, slot.EarliestQueryDeadlineUnixMilli)
	}
}

func TestProductionSlotSourceDoesNotConstructFirstSlotWhenSnapshotIsUnavailable(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{
		t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}, freezeErr: controlplane.ErrSnapshotUnavailable,
	}
	source := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(200, 0))

	slot, due, err := source.Next(context.Background(), "query-group-1")
	if !errors.Is(err, controlplane.ErrSnapshotUnavailable) || due || !reflect.DeepEqual(slot, FrozenSlot{}) {
		t.Fatalf("Next(snapshot unavailable) = (%+v, %v, %v)", slot, due, err)
	}
	var retry *SourceRetryError
	if !errors.As(err, &retry) || fmt.Sprintf("%T", retry.Err) != "*scheduler.slotFreezeSnapshotUnavailableFailure" {
		t.Fatalf("Next(snapshot unavailable) error=%T unwrap=%T, want fixed freeze-contract cause class", err, retry.Err)
	}
	if len(catalog.requests) != 1 {
		t.Fatalf("FreezeSlotContract calls = %d, want 1", len(catalog.requests))
	}
}

func TestProductionSlotSourceRestoresUnfinishedProjectionWhenSnapshotIsUnavailable(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	first := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(200, 0))
	slot, due, err := first.Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("Next(first) = (%+v, %t, %v)", slot, due, err)
	}
	projection := execution.UnfinishedSlotProjection{
		Contract: slot.Contract, DuePlanTargets: slot.DuePlanTargets.Clone(),
		EarliestQueryDeadlineUnixMilli: slot.EarliestQueryDeadlineUnixMilli,
		KeepUntilUnixMilli:             slot.KeepUntilUnixMilli,
	}
	load := foundProgress(slot.ExpectedNextSlot, 0)
	load.Progress.UnfinishedSlot = &projection
	catalog.freezeErr = controlplane.ErrSnapshotUnavailable
	restarted := newProductionSlotSourceForTest(t, catalog, load, time.Unix(200, 0))
	restored, due, err := restarted.Next(context.Background(), "query-group-1")
	if err != nil || !due || restored.Contract != slot.Contract ||
		!restored.DuePlanTargets.Equal(slot.DuePlanTargets) || restored.KeepUntilUnixMilli != slot.KeepUntilUnixMilli {
		t.Fatalf("Next(restarted) = (%+v, %t, %v)", restored, due, err)
	}
}

func TestProductionSlotSourceBlocksCorruptSnapshotWithoutProjection(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule},
		freezeErr: &controlplane.PersistedSnapshotCorruptError{Err: errors.New("bad digest")}}
	source := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(116, 0))
	_, due, err := source.Next(context.Background(), "query-group-1")
	var blocked *SourceBlockedError
	if due || !errors.As(err, &blocked) {
		t.Fatalf("Next(corrupt snapshot) due=%t error=%v", due, err)
	}
	if fmt.Sprintf("%T", blocked.Err) != "*scheduler.slotFreezeSnapshotCorruptFailure" {
		t.Fatalf("Next(corrupt snapshot) unwrap=%T, want fixed freeze-contract cause class", blocked.Err)
	}
}

func TestClassifySlotFreezeFailureUsesFixedLowCardinalityTypes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "snapshot unavailable", err: controlplane.ErrSnapshotUnavailable,
			want: "*scheduler.slotFreezeSnapshotUnavailableFailure"},
		{name: "snapshot corrupt", err: &controlplane.PersistedSnapshotCorruptError{Err: errors.New("sensitive payload")},
			want: "*scheduler.slotFreezeSnapshotCorruptFailure"},
		{name: "schedule unavailable", err: controlplane.ErrScheduleUnavailable,
			want: "*scheduler.slotFreezeScheduleUnavailableFailure"},
		{name: "schedule corrupt", err: &controlplane.DeterministicScheduleError{Err: errors.New("sensitive payload")},
			want: "*scheduler.slotFreezeScheduleCorruptFailure"},
		{name: "catalog object unavailable", err: controlplane.ErrCatalogObjectUnavailable,
			want: "*scheduler.slotFreezeCatalogObjectUnavailableFailure"},
		{name: "schedule read", err: &controlplane.FreezeSlotContractError{
			Class: controlplane.FreezeSlotFailureScheduleRead, Err: errors.New("sensitive payload")},
			want: "*scheduler.slotFreezeScheduleReadFailure"},
		{name: "schedule mismatch", err: &controlplane.FreezeSlotContractError{
			Class: controlplane.FreezeSlotFailureScheduleMismatch, Err: errors.New("sensitive payload")},
			want: "*scheduler.slotFreezeScheduleMismatchFailure"},
		{name: "snapshot read", err: &controlplane.FreezeSlotContractError{
			Class: controlplane.FreezeSlotFailureSnapshotRead, Err: errors.New("sensitive payload")},
			want: "*scheduler.slotFreezeSnapshotReadFailure"},
		{name: "plan materialize", err: &controlplane.FreezeSlotContractError{
			Class: controlplane.FreezeSlotFailurePlanMaterialize, Err: errors.New("sensitive payload")},
			want: "*scheduler.slotFreezePlanMaterializeFailure"},
		{name: "input closure", err: &controlplane.FreezeSlotContractError{
			Class: controlplane.FreezeSlotFailureInputClosure, Err: errors.New("sensitive payload")},
			want: "*scheduler.slotFreezeInputClosureFailure"},
		{name: "contract validation", err: &controlplane.FreezeSlotContractError{
			Class: controlplane.FreezeSlotFailureContractValidation, Err: errors.New("sensitive payload")},
			want: "*scheduler.slotFreezeContractValidationFailure"},
		{name: "other", err: errors.New("sensitive payload"),
			want: "*scheduler.slotFreezeOtherFailure"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifySlotFreezeFailure(test.err)
			if fmt.Sprintf("%T", got) != test.want || !errors.Is(got, test.err) || strings.Contains(got.Error(), "sensitive") ||
				!strings.HasPrefix(got.Error(), slotFreezeFailureMessage+" [class=") {
				t.Fatalf("classifySlotFreezeFailure()=(%T,%q), want type=%s with preserved chain, the class token and safe text", got, got, test.want)
			}
		})
	}
}

// The schedule_due line for a blocked Query Group rendered only the bare
// "FreezeSlotContract failed" text: the class lived in error_type and the
// underlying control cause was lost by the blocked/retry wrapping. The line
// must name the class, the control-plane stage and the bounded cause.
func TestSlotFreezeFailureScheduleDueLineCarriesClassStageAndCause(t *testing.T) {
	cause := classifySlotFreezeFailure(&controlplane.FreezeSlotContractError{
		Class: controlplane.FreezeSlotFailureSnapshotRead, Err: controlplane.ErrSnapshotUnavailable,
	})
	blocked := &SourceBlockedError{Err: cause}
	var output bytes.Buffer
	limiter, err := observability.NewWindowLogLimiter(observability.WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 10})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := observability.NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	observability.NewLoggingObserver(observability.New("alarmd", &output), policy).Observe(context.Background(), observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageScheduleDue,
		Result: observability.ResultRetrying, ReasonCode: observability.ReasonCode(contract.ReasonBlockedExactSetUnavailable),
		Direction: observability.DirectionInternal, Trace: observability.TraceFields{QueryGroupKey: "query-group-1"}, Err: blocked.Err,
	})
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("log line is not JSON: %v: %s", err, output.String())
	}
	const wantError = "alarmd scheduler: FreezeSlotContract failed [class=snapshot_unavailable stage=snapshot_read]: alarmd controlplane: snapshot unavailable"
	if event["error"] != wantError || event["error_type"] != "*scheduler.slotFreezeSnapshotUnavailableFailure" ||
		event["reason_code"] != contract.ReasonBlockedExactSetUnavailable {
		t.Fatalf("schedule_due line = %s, want error %q with its class type", output.String(), wantError)
	}
	// Free-text causes stay out of the line; the class and stage remain.
	opaque := classifySlotFreezeFailure(&controlplane.FreezeSlotContractError{
		Class: controlplane.FreezeSlotFailurePlanMaterialize, Err: errors.New("compiler detail with table names"),
	})
	if opaque.Error() != "alarmd scheduler: FreezeSlotContract failed [class=plan_materialize stage=plan_materialize]" {
		t.Fatalf("opaque cause rendered as %q", opaque.Error())
	}
}

func TestProductionSlotSourceRejectsCandidateOutsideSnapshotRetention(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceWithRecoveryForTest(t, catalog, missingProgress(), time.Unix(116, 0), testRecoveryLimits())
	source.snapshotRetention = 10 * time.Minute
	source.publicationDelayAllowance = time.Minute
	if _, _, err := source.Next(context.Background(), "query-group-1"); !errors.Is(err, ErrSnapshotRetentionInsufficient) {
		t.Fatalf("Next(insufficient retention) error=%v", err)
	}
}

func TestProductionSlotSourceColdStartSkipsClosedZeroSlotSegment(t *testing.T) {
	boundary := execution.EvaluationTime(90)
	oldSchedule := schedulerSchedule(t, 60, 83, &boundary, "snapshot-old", 7)
	newSchedule := schedulerSchedule(t, 60, boundary, nil, "snapshot-new", 8)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{oldSchedule, newSchedule}}
	source := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(200, 0))

	slot, due, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("Next() due=%v error=%v", due, err)
	}
	if slot.Contract.Slot.EvaluationTime != 120 || slot.Contract.ScheduleSegmentStart != boundary ||
		slot.Contract.SnapshotRevision != "snapshot-new" {
		t.Fatalf("cold-start successor Slot=%#v, want new Segment Slot 120", slot.Contract)
	}
}

func TestProductionSlotSourceCrossesCutoverWithoutSecondProgressIdentity(t *testing.T) {
	boundary := execution.EvaluationTime(75)
	oldSchedule := schedulerSchedule(t, 60, 30, &boundary, "snapshot-old", 7)
	newSchedule := schedulerSchedule(t, 90, boundary, nil, "snapshot-new", 8)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{oldSchedule, newSchedule}}
	progress := foundProgress(60, 0)
	source := newProductionSlotSourceForTest(t, catalog, progress, time.Unix(200, 0))

	oldSlot, due, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("old Next() due=%v error=%v", due, err)
	}
	if oldSlot.Contract.ScheduleRevision != oldSchedule.Segment.ScheduleRevision {
		t.Fatalf("old Slot = %+v", oldSlot)
	}
	if !reflect.DeepEqual(catalog.readTimes, []execution.EvaluationTime{60}) {
		t.Fatalf("ReadFrozenSchedule times = %v, want only the current Slot", catalog.readTimes)
	}

	catalog.readTimes = nil
	catalog.requests = nil
	source = newProductionSlotSourceForTest(t, catalog, foundProgress(90, 60), time.Unix(200, 0))
	newSlot, due, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("new Next() due=%v error=%v", due, err)
	}
	if newSlot.Contract.ScheduleRevision != newSchedule.Segment.ScheduleRevision ||
		newSlot.Contract.ScheduleSegmentStart != boundary || newSlot.Contract.Slot.EvaluationTime != 90 {
		t.Fatalf("new Slot = %+v", newSlot)
	}
	if !reflect.DeepEqual(catalog.readTimes, []execution.EvaluationTime{90}) {
		t.Fatalf("ReadFrozenSchedule times = %v, want timeline-proven next Slot", catalog.readTimes)
	}
	if catalog.progressIdentity != (execution.ProgressIdentity{QueryGroup: "query-group-1"}) {
		t.Fatalf("Progress identity = %+v", catalog.progressIdentity)
	}
}

func TestProductionSlotSourceReloadsContinuousNextSlotAfterInflightCutover(t *testing.T) {
	oldSchedule := schedulerSchedule(t, 60, 30, nil, "snapshot-old", 7)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{oldSchedule}}
	control := &slotProgressControlStore{missing: true}
	store, err := progress.NewStore(progress.StoreOptions{
		Prefix: "alarmd", Control: control, Slots: slotProgressResolver{catalog: catalog},
		Now: func() time.Time { return time.Unix(200, 0) },
	})
	if err != nil {
		t.Fatalf("progress.NewStore() error = %v", err)
	}
	source := mustProductionSlotSource(t,
		&fakeAssignmentReader{records: []ownership.AssignmentRecord{testAssignment("worker-1", 3)}},
		&sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}}, catalog, store, time.Unix(200, 0))

	oldSlot, due, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due || oldSlot.Contract.Slot.EvaluationTime != 60 {
		t.Fatalf("freeze old Slot = (%+v, %v, %v)", oldSlot, due, err)
	}

	boundary := execution.EvaluationTime(75)
	oldSchedule.Segment.End = &boundary
	newSchedule := schedulerSchedule(t, 90, boundary, nil, "snapshot-new", 8)
	catalog.schedules = []execution.FrozenQueryGroupSchedule{oldSchedule, newSchedule}
	commit, err := store.CommitProgress(context.Background(), execution.ProgressCommitRequest{
		Identity: execution.ProgressIdentity{QueryGroup: "query-group-1"}, OwnerFence: oldSlot.Dispatch.OwnerFence,
		ExpectedNextSlot: oldSlot.ExpectedNextSlot,
		Completion: execution.SlotCompletion{
			Contract: oldSlot.Contract, Kind: execution.CompletionFull,
			Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData},
			Result:  observability.ResultSuccess,
		},
	})
	if err != nil || commit.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress() = (%+v, %v)", commit, err)
	}
	loaded, err := store.LoadProgress(context.Background(), execution.ProgressIdentity{QueryGroup: "query-group-1"})
	if err != nil || loaded.Progress == nil || loaded.Progress.NextSlot != 90 {
		t.Fatalf("LoadProgress() after cutover = (%+v, %v), want next Slot 90", loaded, err)
	}

	nextSlot, due, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("Next() after cutover due=%v error=%v", due, err)
	}
	if nextSlot.Contract.Slot.EvaluationTime != 90 || nextSlot.Contract.ScheduleRevision != newSchedule.Segment.ScheduleRevision {
		t.Fatalf("next Slot after cutover = %+v, want new Segment Slot 90", nextSlot)
	}
}

func TestProductionSlotSourcePrefersValidProgressCursorWhenCompletedSegmentIsUnavailable(t *testing.T) {
	currentSchedule := schedulerSchedule(t, 60, 180, nil, "snapshot-current", 8)
	tests := []struct {
		name string
		load execution.ProgressLoadResult
	}{
		{name: "last FULL references expired Segment", load: foundProgress(180, 60)},
		{name: "recent Gap references expired Segment", load: nonFullProgress(180, 120,
			execution.CompletionPartialGap, execution.ReasonCode(contract.ReasonHistoryWarming))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{currentSchedule}}
			source := newProductionSlotSourceForTest(t, catalog, test.load, time.Unix(200, 0))

			slot, due, err := source.Next(context.Background(), "query-group-1")
			if err != nil || !due {
				t.Fatalf("Next() due=%v error=%v", due, err)
			}
			if slot.ExpectedNextSlot != 180 || slot.Contract.ScheduleSegmentStart != 180 ||
				slot.Contract.SnapshotRevision != "snapshot-current" {
				t.Fatalf("Next() Slot = %+v, want validated Progress cursor 180", slot)
			}
			if !reflect.DeepEqual(catalog.readTimes, []execution.EvaluationTime{180}) {
				t.Fatalf("ReadFrozenSchedule times = %v, want only Progress cursor 180", catalog.readTimes)
			}
			if len(catalog.nextSlotAfterCalls) != 0 {
				t.Fatalf("NextSlotAfter calls = %v, want no expired completed-Segment navigation", catalog.nextSlotAfterCalls)
			}
		})
	}
}

func TestProductionSlotSourceBlocksInvalidProgressCursorWithoutSuccessorProof(t *testing.T) {
	currentSchedule := schedulerSchedule(t, 60, 180, nil, "snapshot-current", 8)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{currentSchedule}}
	source := newProductionSlotSourceForTest(t, catalog, foundProgress(210, 60), time.Unix(240, 0))

	_, due, err := source.Next(context.Background(), "query-group-1")
	var blocked *SourceBlockedError
	if due || !errors.As(err, &blocked) || !errors.Is(err, ErrProgressOffSchedule) {
		t.Fatalf("Next(invalid cursor) due=%v error=%T %v, want typed fail-closed progress error", due, err, err)
	}
	if !reflect.DeepEqual(catalog.readTimes, []execution.EvaluationTime{210}) {
		t.Fatalf("ReadFrozenSchedule times = %v, want only invalid Progress cursor 210", catalog.readTimes)
	}
	if !reflect.DeepEqual(catalog.nextSlotAfterCalls, []execution.EvaluationTime{60}) {
		t.Fatalf("NextSlotAfter calls = %v, want one bounded successor-proof attempt from 60", catalog.nextSlotAfterCalls)
	}
	if len(catalog.requests) != 0 {
		t.Fatalf("FreezeSlotContract requests = %#v, want no guessed Slot or Gap", catalog.requests)
	}
}

func TestProductionSlotSourceDoesNotMaskCorruptProgressCursorSchedule(t *testing.T) {
	corruptSchedule := schedulerSchedule(t, 60, 180, nil, "snapshot-current", 8)
	corruptSchedule.Segment.ScheduleRevision = "corrupt-schedule-revision"
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{corruptSchedule}}
	source := newProductionSlotSourceForTest(t, catalog, foundProgress(180, 60), time.Unix(200, 0))

	_, due, err := source.Next(context.Background(), "query-group-1")
	var blocked *SourceBlockedError
	if due || !errors.As(err, &blocked) || !errors.Is(err, ErrScheduleFactsInvalid) {
		t.Fatalf("Next(corrupt cursor schedule) due=%v error=%T %v, want typed schedule corruption", due, err, err)
	}
	if len(catalog.nextSlotAfterCalls) != 0 || len(catalog.requests) != 0 {
		t.Fatalf("corrupt cursor schedule used fallback=%v freeze=%#v", catalog.nextSlotAfterCalls, catalog.requests)
	}
}

func TestProductionSlotSourceUsesFirstLegalSlotOnNewCutoverGrid(t *testing.T) {
	boundary := execution.EvaluationTime(75)
	oldSchedule := schedulerSchedule(t, 60, 30, &boundary, "snapshot-old", 7)
	newSchedule := schedulerSchedule(t, 90, boundary, nil, "snapshot-new", 8)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{oldSchedule, newSchedule}}
	load := nonFullProgress(120, 60, execution.CompletionPartialGap,
		execution.ReasonCode(contract.ReasonHistoryWarming))
	source := newProductionSlotSourceForTest(t, catalog, load, time.Unix(200, 0))

	slot, due, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("Next() due=%v error=%v", due, err)
	}
	if slot.ExpectedNextSlot != 90 || slot.Contract.ScheduleRevision != newSchedule.Segment.ScheduleRevision ||
		slot.Contract.ScheduleSegmentStart != boundary {
		t.Fatalf("cutover Slot = %+v, want first legal new-grid Slot 90", slot)
	}
}

func TestProductionSlotSourceTimelineSuccessorCanBeginUnfinishedProgress(t *testing.T) {
	oldSchedule := schedulerSchedule(t, 60, 30, nil, "snapshot-old", 7)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{oldSchedule}}
	control := &slotProgressControlStore{missing: true}
	store, err := progress.NewStore(progress.StoreOptions{
		Prefix: "alarmd", Control: control, Slots: slotProgressResolver{catalog: catalog},
		Now: func() time.Time { return time.Unix(200, 0) },
	})
	if err != nil {
		t.Fatalf("progress.NewStore() error = %v", err)
	}
	source := mustProductionSlotSource(t,
		&fakeAssignmentReader{records: []ownership.AssignmentRecord{testAssignment("worker-1", 3)}},
		&sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}}, catalog, store, time.Unix(200, 0))

	oldSlot, due, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due || oldSlot.ExpectedNextSlot != 60 {
		t.Fatalf("freeze old Slot = (%+v, %v, %v)", oldSlot, due, err)
	}
	committed, err := store.CommitProgress(context.Background(), execution.ProgressCommitRequest{
		Identity: execution.ProgressIdentity{QueryGroup: "query-group-1"}, OwnerFence: oldSlot.Dispatch.OwnerFence,
		ExpectedNextSlot: oldSlot.ExpectedNextSlot,
		Completion: execution.SlotCompletion{
			Contract: oldSlot.Contract, Kind: execution.CompletionFull,
			Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData},
			Result:  observability.ResultSuccess,
		},
	})
	if err != nil || committed.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress(old Slot) = (%+v, %v)", committed, err)
	}
	loaded, err := store.LoadProgress(context.Background(), execution.ProgressIdentity{QueryGroup: "query-group-1"})
	if err != nil || loaded.Progress == nil || loaded.Progress.NextSlot != 120 {
		t.Fatalf("old-grid Progress = (%+v, %v), want NextSlot 120", loaded, err)
	}

	boundary := execution.EvaluationTime(75)
	oldSchedule.Segment.End = &boundary
	newSchedule := schedulerSchedule(t, 90, boundary, nil, "snapshot-new", 8)
	catalog.schedules = []execution.FrozenQueryGroupSchedule{oldSchedule, newSchedule}
	successor, due, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due || successor.ExpectedNextSlot != 90 ||
		successor.Contract.ScheduleRevision != newSchedule.Segment.ScheduleRevision {
		t.Fatalf("freeze timeline successor = (%+v, %v, %v)", successor, due, err)
	}
	projection := execution.UnfinishedSlotProjection{
		Contract: successor.Contract, DuePlanTargets: successor.DuePlanTargets.Clone(),
		EarliestQueryDeadlineUnixMilli: successor.EarliestQueryDeadlineUnixMilli,
		KeepUntilUnixMilli:             successor.KeepUntilUnixMilli,
	}
	begun, err := store.BeginSlot(context.Background(), execution.ProgressBeginRequest{
		Identity:   execution.ProgressIdentity{QueryGroup: "query-group-1"},
		OwnerFence: successor.Dispatch.OwnerFence, Projection: projection,
	})
	if err != nil || begun.Status != execution.ProgressCommitted {
		t.Fatalf("BeginSlot(timeline successor) = (%+v, %v)", begun, err)
	}
	loaded, err = store.LoadProgress(context.Background(), execution.ProgressIdentity{QueryGroup: "query-group-1"})
	if err != nil || loaded.Progress == nil || loaded.Progress.NextSlot != 90 ||
		loaded.Progress.UnfinishedSlot == nil || !loaded.Progress.UnfinishedSlot.Equal(projection) {
		t.Fatalf("successor unfinished Progress = (%+v, %v)", loaded, err)
	}
}

func TestProductionSlotSourceCrossesReactivationForEveryNonFullCompletion(t *testing.T) {
	retiredAt := execution.EvaluationTime(90)
	oldSchedule := schedulerSchedule(t, 60, 60, &retiredAt, "snapshot-old", 7)
	newSchedule := schedulerSchedule(t, 60, 180, nil, "snapshot-new", 9)
	for _, test := range []struct {
		name   string
		kind   execution.CompletionKind
		reason execution.ReasonCode
	}{
		{name: "partial gap", kind: execution.CompletionPartialGap, reason: execution.ReasonCode(contract.ReasonHistoryWarming)},
		{name: "unavailable", kind: execution.CompletionUnavailable, reason: execution.ReasonCode(contract.ReasonQueryUnavailable)},
		{name: "gap skipped", kind: execution.CompletionGapSkipped, reason: execution.ReasonCode(contract.ReasonGapSkipped)},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{oldSchedule, newSchedule}}
			source := newProductionSlotSourceForTest(t, catalog,
				nonFullProgress(retiredAt, 60, test.kind, test.reason), time.Unix(240, 0))

			slot, due, err := source.Next(context.Background(), "query-group-1")
			if err != nil || !due {
				t.Fatalf("Next() due=%v error=%v", due, err)
			}
			if slot.ExpectedNextSlot != 180 || slot.Contract.ScheduleRevision != newSchedule.Segment.ScheduleRevision {
				t.Fatalf("reactivated Slot = %+v, want first legal Slot 180", slot)
			}
		})
	}
}

func TestProductionSlotSourceBoundaryBelongsOnlyToNewSegment(t *testing.T) {
	boundary := execution.EvaluationTime(120)
	oldSchedule := schedulerSchedule(t, 60, 60, &boundary, "snapshot-old", 7)
	newSchedule := schedulerSchedule(t, 60, boundary, nil, "snapshot-new", 8)
	if due := oldSchedule.DuePlanRefs(boundary); len(due) != 0 {
		t.Fatalf("old Segment owns boundary: %+v", due)
	}
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{oldSchedule, newSchedule}}
	source := newProductionSlotSourceForTest(t, catalog, foundProgress(boundary, 60), time.Unix(200, 0))

	slot, due, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("Next() due=%v error=%v", due, err)
	}
	if slot.Contract.ScheduleRevision != newSchedule.Segment.ScheduleRevision || slot.Contract.ScheduleSegmentStart != boundary {
		t.Fatalf("boundary Slot provenance = %+v", slot.Contract)
	}
}

func TestProductionSlotSourceRetiredProgressHasNoSuccessorSlot(t *testing.T) {
	boundary := execution.EvaluationTime(90)
	schedule := schedulerSchedule(t, 60, 60, &boundary, "snapshot-retired", 9)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}, retiredAt: &boundary}
	source := newProductionSlotSourceForTest(t, catalog, foundProgress(boundary, 60), time.Unix(200, 0))

	_, due, err := source.Next(context.Background(), "query-group-1")
	if err != nil || due {
		t.Fatalf("retired Next() due=%v error=%v", due, err)
	}
	if len(catalog.requests) != 0 {
		t.Fatalf("retired Query Group froze a successor Slot: %#v", catalog.requests)
	}
}

func TestProductionSlotSourceRetiredZeroSlotSegmentHasNoFabricatedSlot(t *testing.T) {
	boundary := execution.EvaluationTime(90)
	schedule := schedulerSchedule(t, 60, 83, &boundary, "snapshot-retired", 9)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}, retiredAt: &boundary}
	source := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(200, 0))

	_, due, err := source.Next(context.Background(), "query-group-1")
	if err != nil || due {
		t.Fatalf("zero-Slot retired Next() due=%v error=%v", due, err)
	}
	if len(catalog.requests) != 0 {
		t.Fatalf("zero-Slot retired Segment froze a Slot: %#v", catalog.requests)
	}
}

func TestProductionSlotSourceRestartFreezesSameContract(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	load := foundProgress(120, 60)
	firstCatalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	secondCatalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	first := newProductionSlotSourceForTest(t, firstCatalog, load, time.Unix(200, 0))
	second := newProductionSlotSourceForTest(t, secondCatalog, load, time.Unix(200, 0))

	firstSlot, firstDue, firstErr := first.Next(context.Background(), "query-group-1")
	secondSlot, secondDue, secondErr := second.Next(context.Background(), "query-group-1")
	if firstErr != nil || secondErr != nil || !firstDue || !secondDue {
		t.Fatalf("restart first=(%v,%v) second=(%v,%v)", firstDue, firstErr, secondDue, secondErr)
	}
	if !reflect.DeepEqual(firstSlot, secondSlot) {
		t.Fatalf("contracts drifted across restart: first=%+v second=%+v", firstSlot, secondSlot)
	}
}

func TestProductionSlotSourceRechecksOwnershipAfterFreeze(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	assignments := &fakeAssignmentReader{records: []ownership.AssignmentRecord{testAssignment("worker-1", 3), testAssignment("worker-1", 4)}}
	source := mustProductionSlotSource(t, assignments, &sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}},
		catalog, &fakeProgressReader{result: missingProgress(), catalog: catalog}, time.Unix(200, 0))

	if _, _, err := source.Next(context.Background(), "query-group-1"); !errors.Is(err, ErrSlotOwnershipChanged) {
		t.Fatalf("Next() error = %v, want ErrSlotOwnershipChanged", err)
	}
}

func TestProductionSlotSourceDoesNotFreezeFutureSlot(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(59, 0))

	if _, due, err := source.Next(context.Background(), "query-group-1"); err != nil || due {
		t.Fatalf("Next() due=%v error=%v", due, err)
	}
	if len(catalog.requests) != 0 {
		t.Fatalf("FreezeSlotContract calls = %d, want 0", len(catalog.requests))
	}
}

func TestProductionSlotSourceMarksEligibleBacklogAsReplayWithoutChangingContract(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	limits := testRecoveryLimits()
	source := newProductionSlotSourceWithRecoveryForTest(t, catalog, missingProgress(), time.Unix(200, 0), limits)

	slot, due, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("Next() due=%v error=%v", due, err)
	}
	if slot.Dispatch.Operation != execution.OperationReplay || slot.Recovery.Disposition != ReplayEligible ||
		slot.Recovery.Distance != 3 {
		t.Fatalf("backlog slot recovery facts = %+v dispatch=%+v", slot.Recovery, slot.Dispatch)
	}
	if slot.Contract.Slot.EvaluationTime != 60 || slot.ExpectedNextSlot != 60 {
		t.Fatalf("replay changed frozen Slot identity: %+v", slot)
	}
}

func TestProductionSlotSourceStartsReplayAtFrozenQueryDeadline(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	limits := testRecoveryLimits()
	source := newProductionSlotSourceWithRecoveryForTest(t, catalog, missingProgress(), time.Unix(116, 0), limits)

	slot, due, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("Next() due=%v error=%v", due, err)
	}
	if slot.Dispatch.Operation != execution.OperationReplay || slot.Recovery.Disposition != ReplayEligible ||
		slot.Recovery.Distance != 1 || slot.Recovery.Age != time.Second {
		t.Fatalf("query-deadline replay facts = %+v dispatch=%+v", slot.Recovery, slot.Dispatch)
	}
}

func TestProductionSlotSourceLeavesExpiredBacklogForExistingGapFinalizer(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	limits := testRecoveryLimits()
	limits.MaxReplaySlots = 2
	source := newProductionSlotSourceWithRecoveryForTest(t, catalog, missingProgress(), time.Unix(200, 0), limits)

	slot, due, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("Next() due=%v error=%v", due, err)
	}
	if slot.Dispatch.Operation != execution.OperationNormal || slot.Recovery.Disposition != ReplayExpired ||
		slot.Recovery.Distance != 3 {
		t.Fatalf("expired backlog recovery facts = %+v dispatch=%+v", slot.Recovery, slot.Dispatch)
	}
}

func TestProductionSlotSourceExpiresRetiredBacklogByAgeWithoutReadingPastBoundary(t *testing.T) {
	boundary := execution.EvaluationTime(137)
	schedule := schedulerSchedule(t, 60, 60, &boundary, "snapshot-retired", 9)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}, retiredAt: &boundary}
	limits := testRecoveryLimits()
	source := newProductionSlotSourceWithRecoveryForTest(t, catalog, foundProgress(120, 60), time.Unix(1000, 0), limits)

	slot, due, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("Next() due=%v error=%v", due, err)
	}
	if slot.Dispatch.Operation != execution.OperationNormal || slot.Recovery.Disposition != ReplayExpired ||
		slot.Recovery.Distance != 1 || slot.Recovery.Age <= limits.MaxReplayAge {
		t.Fatalf("age-expired retired Slot recovery facts = %+v dispatch=%+v", slot.Recovery, slot.Dispatch)
	}
	if len(catalog.nextSlotAfterCalls) != 0 {
		t.Fatalf("NextSlotAfter calls = %v, want no historical completed-Segment navigation", catalog.nextSlotAfterCalls)
	}
}

func TestProductionSlotSourceReplayDistanceStopsAtRetirementBoundary(t *testing.T) {
	boundary := execution.EvaluationTime(137)
	schedule := schedulerSchedule(t, 60, 60, &boundary, "snapshot-retired", 9)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}, retiredAt: &boundary}
	source := newProductionSlotSourceWithRecoveryForTest(t, catalog, foundProgress(120, 60), time.Unix(200, 0), testRecoveryLimits())

	slot, due, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("Next() due=%v error=%v", due, err)
	}
	if slot.Dispatch.Operation != execution.OperationReplay || slot.Recovery.Disposition != ReplayEligible ||
		slot.Recovery.Distance != 1 {
		t.Fatalf("retired Slot recovery facts = %+v dispatch=%+v", slot.Recovery, slot.Dispatch)
	}
	if !reflect.DeepEqual(catalog.nextSlotAfterCalls, []execution.EvaluationTime{120}) {
		t.Fatalf("NextSlotAfter calls = %v, want no read at retirement boundary %d", catalog.nextSlotAfterCalls, boundary)
	}
}

func schedulerSchedule(
	t *testing.T,
	interval int64,
	start execution.EvaluationTime,
	end *execution.EvaluationTime,
	snapshot execution.SnapshotRevision,
	epoch execution.PublicationEpoch,
) execution.FrozenQueryGroupSchedule {
	t.Helper()
	spec := execution.ScheduleSpec{EvaluationIntervalSeconds: interval, Alignment: 0, Timezone: "UTC"}
	plan := execution.FrozenPlanSchedule{Identity: planIdentity("1"), ScheduleRevision: mustPlanScheduleRevision(t, spec), Spec: spec}
	revision, err := execution.DeriveQueryGroupScheduleRevision([]execution.FrozenPlanSchedule{plan})
	if err != nil {
		t.Fatalf("DeriveQueryGroupScheduleRevision() error = %v", err)
	}
	return execution.FrozenQueryGroupSchedule{
		Segment: execution.ScheduleSegmentFact{
			Publication: execution.SnapshotPublicationRef{SnapshotRevision: snapshot, PublicationEpoch: epoch},
			QueryGroup:  "query-group-1", QueryRevision: "query-1", ScheduleRevision: revision, Start: start, End: end,
		},
		Plans: []execution.FrozenPlanSchedule{plan},
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

func planIdentity(strategyID string) execution.PlanIdentity {
	return execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: strategyID}
}

func missingProgress() execution.ProgressLoadResult {
	return execution.ProgressLoadResult{Status: execution.ProgressMissing}
}

func foundProgress(nextSlot, lastFull execution.EvaluationTime) execution.ProgressLoadResult {
	return execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: "query-group-1"}, NextSlot: nextSlot,
		LastFullSlot: lastFull, LastCompletionKind: execution.CompletionFull,
	}}
}

func nonFullProgress(
	nextSlot, lastCompleted execution.EvaluationTime,
	kind execution.CompletionKind,
	reason execution.ReasonCode,
) execution.ProgressLoadResult {
	return execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: "query-group-1"}, NextSlot: nextSlot,
		LastCompletionKind: kind,
		CurrentOrRecentGap: &execution.ProgressGapSummary{
			Kind: kind, ReasonCode: reason, FirstSlot: lastCompleted, LastSlot: lastCompleted, Count: 1,
		},
	}}
}

func testAssignment(workerID string, generation uint64) ownership.AssignmentRecord {
	return ownership.AssignmentRecord{QueryGroup: "query-group-1", DesiredWorkerID: workerID,
		AssignmentGeneration: generation, RecordRevision: generation, ControlEpoch: 2,
		PlacementReason: ownership.PlacementRendezvous, AssignedAt: time.Unix(50, 0)}
}

func testFence(epoch uint64) execution.OwnerFence {
	return execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: epoch, LeaseToken: "lease-token"}
}

func newProductionSlotSourceForTest(
	t *testing.T,
	catalog *fakeSlotCatalog,
	load execution.ProgressLoadResult,
	at time.Time,
) *ProductionSlotSource {
	t.Helper()
	reader := &fakeProgressReader{result: load, catalog: catalog}
	return mustProductionSlotSource(t, &fakeAssignmentReader{records: []ownership.AssignmentRecord{testAssignment("worker-1", 3)}},
		&sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}}, catalog, reader, at)
}

func newProductionSlotSourceWithRecoveryForTest(
	t *testing.T,
	catalog *fakeSlotCatalog,
	load execution.ProgressLoadResult,
	at time.Time,
	limits RecoveryLimits,
) *ProductionSlotSource {
	t.Helper()
	reader := &fakeProgressReader{result: load, catalog: catalog}
	source, err := NewProductionSlotSource("query-group-1", "worker-1",
		&fakeAssignmentReader{records: []ownership.AssignmentRecord{testAssignment("worker-1", 3)}},
		&sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}}, catalog, reader,
		func() time.Time { return at }, WithRecoveryLimits(limits), WithPostRecoveryTerminalDelay(time.Minute), WithQueryDeadlineReserve(5*time.Second))
	if err != nil {
		t.Fatalf("NewProductionSlotSource() error = %v", err)
	}
	return source
}

func mustProductionSlotSource(
	t *testing.T,
	assignments AssignmentReader,
	session OwnerSession,
	catalog SlotCatalogReader,
	progress ScheduleProgressReader,
	at time.Time,
) *ProductionSlotSource {
	t.Helper()
	source, err := NewProductionSlotSource("query-group-1", "worker-1", assignments, session, catalog, progress, func() time.Time { return at },
		WithRecoveryLimits(testRecoveryLimits()), WithPostRecoveryTerminalDelay(time.Minute), WithQueryDeadlineReserve(5*time.Second))
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
}

func (session *sequenceOwnerSession) ValidateCurrent(context.Context, time.Time) (execution.OwnerFence, error) {
	index := session.reads
	if index >= len(session.fences) {
		index = len(session.fences) - 1
	}
	session.reads++
	return session.fences[index], nil
}

// This fake feeds a SlotSource directly, without a Runner in front of it, so it
// carries no Assignment facts to hand forward and the source keeps reading the
// Assignment itself. Consuming a fence from the sequence here would move the
// sequence the ownership recheck tests depend on.
func (session *sequenceOwnerSession) ValidateCurrentWithAssignment(
	ctx context.Context,
	at time.Time,
) (execution.OwnerFence, ownership.AssignmentRecord, error) {
	fence, err := session.ValidateCurrent(ctx, at)
	return fence, ownership.AssignmentRecord{}, err
}

type fakeSlotCatalog struct {
	t                  *testing.T
	schedules          []execution.FrozenQueryGroupSchedule
	initialReads       int
	readTimes          []execution.EvaluationTime
	nextSlotAfterCalls []execution.EvaluationTime
	requests           []execution.FreezeSlotContractRequest
	progressIdentity   execution.ProgressIdentity
	retiredAt          *execution.EvaluationTime
	freezeErr          error
}

func (catalog *fakeSlotCatalog) ReadInitialFrozenSchedule(
	_ context.Context,
	queryGroup execution.QueryGroupIdentity,
) (execution.FrozenQueryGroupSchedule, error) {
	catalog.initialReads++
	if len(catalog.schedules) == 0 || catalog.schedules[0].Segment.QueryGroup != queryGroup {
		return execution.FrozenQueryGroupSchedule{}, ErrScheduleFactsInvalid
	}
	return catalog.schedules[0], nil
}

func (catalog *fakeSlotCatalog) ReadFrozenSchedule(
	_ context.Context,
	queryGroup execution.QueryGroupIdentity,
	at execution.EvaluationTime,
) (execution.FrozenQueryGroupSchedule, error) {
	catalog.readTimes = append(catalog.readTimes, at)
	for _, schedule := range catalog.schedules {
		if schedule.Segment.QueryGroup == queryGroup && schedule.Segment.Contains(at) {
			return schedule, nil
		}
	}
	return execution.FrozenQueryGroupSchedule{}, ErrProgressOffSchedule
}

func (catalog *fakeSlotCatalog) ReadSuccessorFrozenSchedule(
	_ context.Context,
	queryGroup execution.QueryGroupIdentity,
	segmentEnd execution.EvaluationTime,
) (execution.FrozenQueryGroupSchedule, error) {
	for index := 1; index < len(catalog.schedules); index++ {
		previous := catalog.schedules[index-1]
		if previous.Segment.QueryGroup == queryGroup && previous.Segment.End != nil && *previous.Segment.End == segmentEnd {
			return catalog.schedules[index], nil
		}
	}
	return execution.FrozenQueryGroupSchedule{}, ErrProgressOffSchedule
}

func (catalog *fakeSlotCatalog) FreezeSlotContract(
	_ context.Context,
	request execution.FreezeSlotContractRequest,
) (execution.FrozenSlotContractFact, error) {
	catalog.requests = append(catalog.requests, request)
	if catalog.freezeErr != nil {
		return execution.FrozenSlotContractFact{}, catalog.freezeErr
	}
	var schedule execution.FrozenQueryGroupSchedule
	for _, candidate := range catalog.schedules {
		if candidate.Segment.Start == request.ScheduleSegmentStart && candidate.Segment.QueryGroup == request.QueryGroup {
			schedule = candidate
			break
		}
	}
	return frozenSlotContractFact(catalog.t, schedule, request), nil
}

func (catalog *fakeSlotCatalog) ReadScheduleRetirement(
	context.Context,
	execution.QueryGroupIdentity,
) (execution.EvaluationTime, bool, error) {
	if catalog.retiredAt == nil {
		return 0, false, nil
	}
	return *catalog.retiredAt, true, nil
}

func (catalog *fakeSlotCatalog) NextSlotAfter(
	_ context.Context,
	queryGroup execution.QueryGroupIdentity,
	completed execution.EvaluationTime,
) (execution.EvaluationTime, error) {
	catalog.nextSlotAfterCalls = append(catalog.nextSlotAfterCalls, completed)
	for index, schedule := range catalog.schedules {
		if schedule.Segment.QueryGroup != queryGroup || !schedule.Segment.Contains(completed) {
			continue
		}
		if next, ok := schedule.NextSlotAfter(completed); ok {
			return next, nil
		}
		for successor := index + 1; successor < len(catalog.schedules); successor++ {
			if next, ok := catalog.schedules[successor].FirstSlot(); ok {
				return next, nil
			}
		}
	}
	if catalog.retiredAt != nil && *catalog.retiredAt > completed {
		return *catalog.retiredAt, nil
	}
	return 0, ErrProgressOffSchedule
}

func frozenSlotContractFact(
	t *testing.T,
	schedule execution.FrozenQueryGroupSchedule,
	request execution.FreezeSlotContractRequest,
) execution.FrozenSlotContractFact {
	t.Helper()
	compiled := compiledPlanForSlotSource(t)
	plans := make([]execution.DuePlan, len(request.DuePlans))
	requirements := make([]execution.DataRequirement, len(request.DuePlans))
	for index, ref := range request.DuePlans {
		var spec execution.ScheduleSpec
		for _, plan := range schedule.Plans {
			if plan.Identity == ref.Identity && plan.ScheduleRevision == ref.ScheduleRevision {
				spec = plan.Spec
				break
			}
		}
		deadline := (int64(request.EvaluationTime) + spec.EvaluationIntervalSeconds) * 1000
		plans[index] = execution.DuePlan{
			Identity: ref.Identity, CompiledPlan: compiled, StateGeneration: "state-v1", StateApplyEpoch: 1,
			ScheduleRevision: ref.ScheduleRevision, ScheduleSpec: spec, CompletionDeadlineUnixMilli: deadline,
		}
		requirementID := execution.RequirementID("primary-" + ref.Identity.StrategyID)
		requirements[index] = execution.DataRequirement{
			RequirementID: requirementID, DatasetName: execution.DatasetName(requirementID), Role: execution.InputRolePrimary,
			LogicalQueryRef: "query-main",
			RelativeWindow:  execution.RelativeQueryWindow{StartOffsetSeconds: -60, EndOffsetSeconds: 0, HalfOpen: true},
			StepMillis:      60_000, AlignmentMillis: 60_000, ResultWindowPolicy: execution.ResultWindowExactHalfOpen,
			ReadinessClass: execution.ReadinessEager, RequiredColumns: []string{"value"},
			Consumers: []execution.DataRequirementConsumer{{
				Consumer: execution.ConsumerRef{Plan: ref.Identity}, ConsumerDeadlineUnixMilli: deadline,
				DownstreamExecutionReserveMilliSec: 5_000,
			}},
		}
	}
	digest, err := execution.DeriveDuePlanSetDigest(plans, requirements)
	if err != nil {
		t.Fatalf("DeriveDuePlanSetDigest() error = %v", err)
	}
	return execution.FrozenSlotContractFact{
		Contract: execution.FrozenExecutionContractRef{
			Slot:             execution.SlotIdentity{QueryGroup: request.QueryGroup, EvaluationTime: request.EvaluationTime},
			SnapshotRevision: schedule.Segment.Publication.SnapshotRevision, QueryRevision: schedule.Segment.QueryRevision,
			ScheduleRevision: request.ScheduleRevision, ScheduleSegmentStart: request.ScheduleSegmentStart,
			DuePlanSetDigest: digest,
		},
		DuePlans: plans, Requirements: requirements,
	}
}

func compiledPlanForSlotSource(t *testing.T) *strategy.CompiledPlan {
	t.Helper()
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{
		MaxPlanBytes: 64 << 10, MaxLevelsPerPlan: 4, MaxAlgorithmsPerLevel: 4, MaxGroupsPerAlgorithm: 4,
		MaxConditionsPerAlgorithm: 8, MaxASTNodesPerLevel: 32, MaxTriggerWindowSize: 64,
		MaxRecoveryConsecutiveWindows: 64, MaxRequiredHistoryPoints: 64, MaxTriggerComputeCost: 1 << 16,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 4, MaxCacheBytes: 1 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "scheduler-test-v1",
	})
	if err != nil {
		t.Fatalf("NewCompiler() error = %v", err)
	}
	ref := contract.StrategyRefV2{TenantID: "tenant", StrategyID: "1", Revision: "r1"}
	projection := contract.InputProjectionV2{
		ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id",
		MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired,
	}
	plan := contract.EvaluationPlanV2{
		PlanID: "1", StrategyRef: ref, InputProjection: projection,
		StrategyIR: contract.StrategyIRV2{
			Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2}, StrategyRef: ref,
			InputProjection: projection,
			ExecutionSemantics: contract.ExecutionSemanticsV2{
				EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 60,
				AggregationInterval: 60, EvaluationInterval: 60,
			},
			Levels: []contract.LevelIRV2{{
				Definition: contract.LevelDefinitionV2{LevelID: 1, Priority: 1}, Connector: contract.LevelConnectorAND,
				DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{
					Type: "Threshold", Version: 1,
					Config: json.RawMessage(`{"value_field":"value","data_unit":"percent","threshold_unit_prefix":"","precision":{"decimal_places":6,"rounding":"HALF_EVEN"},"groups":[{"conditions":[{"operator":"GTE","threshold_decimal":"50"}]}]}`),
				}}},
				TriggerPlan: contract.TypedPlanV1{Type: "N_OF_M", Version: 1,
					Config: json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)},
				RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1,
					Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)},
			}},
		},
	}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{
		Plan: plan,
		DatasetContract: contract.DatasetContractV2{
			SchemaDigest: strings.Repeat("a", 64), NormalizationDigest: strings.Repeat("b", 64),
			IdentityFields: []string{"host"}, SourceTimeField: "_time", ReceivedTimeField: "_received_time",
		},
		StateSemantics: strategy.StateSemantics{
			StateSchemaVersion: "v1", CodecSemanticsVersion: "v1", IdentitySchemaDigest: strings.Repeat("c", 64),
			SourceTimeSemanticsVersion: "seconds-v1", HistoryCellSemanticsVersion: "v1",
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("Compile() terminal = %+v", result.PlanTerminal())
	}
	return compiled
}

type fakeProgressReader struct {
	result  execution.ProgressLoadResult
	catalog *fakeSlotCatalog
}

func (reader *fakeProgressReader) LoadProgress(
	_ context.Context,
	identity execution.ProgressIdentity,
) (execution.ProgressLoadResult, error) {
	reader.catalog.progressIdentity = identity
	return reader.result, nil
}

type slotProgressControlStore struct {
	value   []byte
	missing bool
}

type slotProgressResolver struct {
	catalog *fakeSlotCatalog
}

func (resolver slotProgressResolver) NextSlotAfter(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	completed execution.EvaluationTime,
) (execution.EvaluationTime, error) {
	schedule, err := resolver.catalog.ReadFrozenSchedule(ctx, queryGroup, completed)
	if err != nil {
		return 0, err
	}
	if next, ok := schedule.NextSlotAfter(completed); ok {
		return next, nil
	}
	if schedule.Segment.End == nil {
		return 0, ErrScheduleFactsInvalid
	}
	successor, err := resolver.catalog.ReadFrozenSchedule(ctx, queryGroup, *schedule.Segment.End)
	if err != nil {
		return 0, err
	}
	next, ok := successor.FirstSlot()
	if !ok {
		return 0, ErrScheduleFactsInvalid
	}
	return next, nil
}

func (store *slotProgressControlStore) ReadControl(
	context.Context,
	execution.QueryGroupIdentity,
	string,
) ([]byte, bool, error) {
	return append([]byte(nil), store.value...), store.missing, nil
}

func (store *slotProgressControlStore) FencedCompareAndSet(
	_ context.Context,
	request ownership.FencedCASRequest,
) (ownership.FencedCASStatus, error) {
	store.value = append([]byte(nil), request.Value...)
	store.missing = false
	return ownership.FencedCASApplied, nil
}
