// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package progress

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

type controlFake struct {
	value   []byte
	missing bool
	status  ownership.FencedCASStatus
}

func TestCommitProgressUsesExplicitNextSlotAndFence(t *testing.T) {
	fake := &controlFake{missing: true}
	store := mustStore(t, fake)
	ref := execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "q", ScheduleRevision: "r", EvaluationTime: 60}, SnapshotRevision: "s", QueryRevision: "query", ScheduleRevision: "r", DuePlanSetDigest: "plans"}
	request := execution.ProgressCommitRequest{
		Namespace:        execution.ProgressNamespace{QueryGroup: "q", ScheduleRevision: "r"},
		OwnerFence:       execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"},
		ExpectedNextSlot: 60, NextSlotAfterCompletion: 120,
		Completion: execution.SlotCompletion{Contract: ref, Kind: execution.CompletionFull,
			Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData}, Result: observability.ResultSuccess},
	}
	result, err := store.CommitProgress(context.Background(), request)
	if err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress() = (%+v, %v)", result, err)
	}
	loaded, err := store.LoadProgress(context.Background(), request.Namespace)
	if err != nil || loaded.Progress.NextSlot != 120 || loaded.Progress.LastFullSlot != 60 {
		t.Fatalf("LoadProgress() = (%+v, %v)", loaded, err)
	}
}

func TestLoadProgressRejectsCorruptValueWithoutTreatingItAsMissing(t *testing.T) {
	store := mustStore(t, &controlFake{value: []byte("not-json")})
	result, err := store.LoadProgress(context.Background(), execution.ProgressNamespace{QueryGroup: "q", ScheduleRevision: "r"})
	if err == nil || result.Status == execution.ProgressMissing {
		t.Fatalf("LoadProgress(corrupt) = (%+v, %v)", result, err)
	}
	if _, ok := err.(*DeterministicInvalidError); !ok {
		t.Fatalf("error type = %T", err)
	}
}

func TestProgressNamespaceMismatchIsDeterministicInvalidAndNotOverwritten(t *testing.T) {
	requested := execution.ProgressNamespace{QueryGroup: "q", ScheduleRevision: "r"}
	other := execution.ProgressNamespace{QueryGroup: "q", ScheduleRevision: "other"}
	raw := mustEncode(t, execution.ScheduleProgress{Namespace: other, NextSlot: 60})
	fake := &controlFake{value: append([]byte(nil), raw...)}
	store := mustStore(t, fake)
	if _, err := store.LoadProgress(context.Background(), requested); err == nil {
		t.Fatal("LoadProgress accepted mismatched namespace")
	}
	ref := execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "q", ScheduleRevision: "r", EvaluationTime: 60}, SnapshotRevision: "s", QueryRevision: "query", ScheduleRevision: "r", DuePlanSetDigest: "plans"}
	request := execution.ProgressCommitRequest{Namespace: requested, OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "w", OwnerEpoch: 1, LeaseToken: "l"}, ExpectedNextSlot: 60, NextSlotAfterCompletion: 120, Completion: execution.SlotCompletion{Contract: ref, Kind: execution.CompletionFull, Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData}, Result: observability.ResultSuccess}}
	if _, err := store.CommitProgress(context.Background(), request); err == nil {
		t.Fatal("CommitProgress accepted mismatched namespace")
	}
	if string(fake.value) != string(raw) {
		t.Fatal("mismatched Progress was overwritten")
	}
}

func TestCommitProgressMapsFencedCASStatuses(t *testing.T) {
	for _, test := range []struct {
		cas  ownership.FencedCASStatus
		want execution.ProgressCommitStatus
	}{
		{ownership.FencedCASConflict, execution.ProgressConflict},
		{ownership.FencedCASStaleOwner, execution.ProgressStaleOwner},
	} {
		fake := &controlFake{missing: true, status: test.cas}
		store := mustStore(t, fake)
		ref := execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "q", ScheduleRevision: "r", EvaluationTime: 60}, SnapshotRevision: "s", QueryRevision: "query", ScheduleRevision: "r", DuePlanSetDigest: "plans"}
		request := execution.ProgressCommitRequest{Namespace: execution.ProgressNamespace{QueryGroup: "q", ScheduleRevision: "r"}, OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "w", OwnerEpoch: 1, LeaseToken: "l"}, ExpectedNextSlot: 60, NextSlotAfterCompletion: 120, Completion: execution.SlotCompletion{Contract: ref, Kind: execution.CompletionFull, Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData}, Result: observability.ResultSuccess}}
		result, err := store.CommitProgress(context.Background(), request)
		if err != nil || result.Status != test.want {
			t.Fatalf("CommitProgress(%s) = (%+v, %v)", test.cas, result, err)
		}
	}
}

func (fake *controlFake) ReadControl(context.Context, execution.QueryGroupIdentity, string) ([]byte, bool, error) {
	return append([]byte(nil), fake.value...), fake.missing, nil
}
func (fake *controlFake) FencedCompareAndSet(_ context.Context, request ownership.FencedCASRequest) (ownership.FencedCASStatus, error) {
	fake.value, fake.missing = append([]byte(nil), request.Value...), false
	if fake.status == "" {
		return ownership.FencedCASApplied, nil
	}
	return fake.status, nil
}

func TestLoadProgressDistinguishesMissingAndFound(t *testing.T) {
	fake := &controlFake{missing: true}
	store := mustStore(t, fake)
	namespace := execution.ProgressNamespace{QueryGroup: "q", ScheduleRevision: "r"}
	missing, err := store.LoadProgress(context.Background(), namespace)
	if err != nil || missing.Status != execution.ProgressMissing {
		t.Fatalf("missing = (%+v, %v)", missing, err)
	}
	fake.value = mustEncode(t, execution.ScheduleProgress{Namespace: namespace, NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull})
	fake.missing = false
	found, err := store.LoadProgress(context.Background(), namespace)
	if err != nil || found.Status != execution.ProgressFound || found.Progress.NextSlot != 120 {
		t.Fatalf("found = (%+v, %v)", found, err)
	}
}

func mustStore(t *testing.T, control ControlStore) *Store {
	t.Helper()
	store, err := NewStore(StoreOptions{Prefix: "alarmd", Control: control, Now: func() time.Time { return time.Unix(1, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func mustEncode(t *testing.T, value execution.ScheduleProgress) []byte {
	t.Helper()
	raw, err := encode(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
