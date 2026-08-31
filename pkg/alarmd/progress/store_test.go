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
	group   execution.QueryGroupIdentity
	name    string
}

func TestCommitProgressUsesExplicitNextSlotAndFence(t *testing.T) {
	fake := &controlFake{missing: true}
	store := mustStore(t, fake)
	ref := progressContract()
	request := execution.ProgressCommitRequest{
		Identity:         execution.ProgressIdentity{QueryGroup: "q"},
		OwnerFence:       execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"},
		ExpectedNextSlot: 60,
		Completion: execution.SlotCompletion{Contract: ref, Kind: execution.CompletionFull,
			Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData}, Result: observability.ResultSuccess},
	}
	result, err := store.CommitProgress(context.Background(), request)
	if err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress() = (%+v, %v)", result, err)
	}
	loaded, err := store.LoadProgress(context.Background(), request.Identity)
	if err != nil || loaded.Progress.NextSlot != 120 || loaded.Progress.LastFullSlot != 60 {
		t.Fatalf("LoadProgress() = (%+v, %v)", loaded, err)
	}
}

func TestLoadProgressRejectsCorruptValueWithoutTreatingItAsMissing(t *testing.T) {
	store := mustStore(t, &controlFake{value: []byte("not-json")})
	result, err := store.LoadProgress(context.Background(), execution.ProgressIdentity{QueryGroup: "q"})
	if err == nil || result.Status == execution.ProgressMissing {
		t.Fatalf("LoadProgress(corrupt) = (%+v, %v)", result, err)
	}
	if _, ok := err.(*DeterministicInvalidError); !ok {
		t.Fatalf("error type = %T", err)
	}
}

func TestProgressIdentityMismatchIsDeterministicInvalidAndNotOverwritten(t *testing.T) {
	requested := execution.ProgressIdentity{QueryGroup: "q"}
	other := execution.ProgressIdentity{QueryGroup: "other"}
	raw := mustEncode(t, execution.ScheduleProgress{Identity: other, NextSlot: 60})
	fake := &controlFake{value: append([]byte(nil), raw...)}
	store := mustStore(t, fake)
	if _, err := store.LoadProgress(context.Background(), requested); err == nil {
		t.Fatal("LoadProgress accepted mismatched identity")
	}
	request := execution.ProgressCommitRequest{Identity: requested, OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "w", OwnerEpoch: 1, LeaseToken: "l"}, ExpectedNextSlot: 60, Completion: execution.SlotCompletion{Contract: progressContract(), Kind: execution.CompletionFull, Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData}, Result: observability.ResultSuccess}}
	if _, err := store.CommitProgress(context.Background(), request); err == nil {
		t.Fatal("CommitProgress accepted mismatched identity")
	}
	if string(fake.value) != string(raw) {
		t.Fatal("mismatched Progress was overwritten")
	}
}

func TestCommitProgressRepairsStaleNextSlotFromCurrentSegmentFacts(t *testing.T) {
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	raw := mustEncode(t, execution.ScheduleProgress{
		Identity: identity, NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull,
	})
	fake := &controlFake{value: raw}
	store := mustStoreWithSlots(t, fake, mappedSlotResolver{60: 90, 90: 180})
	request := execution.ProgressCommitRequest{
		Identity: identity, OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "w", OwnerEpoch: 1, LeaseToken: "l"},
		ExpectedNextSlot: 90,
		Completion: execution.SlotCompletion{
			Contract: progressContractAt(90), Kind: execution.CompletionFull,
			Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData},
			Result:  observability.ResultSuccess,
		},
	}
	result, err := store.CommitProgress(context.Background(), request)
	if err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress() = (%+v, %v), want stale next repaired", result, err)
	}
	loaded, err := store.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil || loaded.Progress.NextSlot != 180 {
		t.Fatalf("LoadProgress() = (%+v, %v), want next Slot 180", loaded, err)
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
		request := execution.ProgressCommitRequest{Identity: execution.ProgressIdentity{QueryGroup: "q"}, OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "w", OwnerEpoch: 1, LeaseToken: "l"}, ExpectedNextSlot: 60, Completion: execution.SlotCompletion{Contract: progressContract(), Kind: execution.CompletionFull, Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData}, Result: observability.ResultSuccess}}
		result, err := store.CommitProgress(context.Background(), request)
		if err != nil || result.Status != test.want {
			t.Fatalf("CommitProgress(%s) = (%+v, %v)", test.cas, result, err)
		}
	}
}

func (fake *controlFake) ReadControl(_ context.Context, group execution.QueryGroupIdentity, name string) ([]byte, bool, error) {
	fake.group, fake.name = group, name
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
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	missing, err := store.LoadProgress(context.Background(), identity)
	if err != nil || missing.Status != execution.ProgressMissing {
		t.Fatalf("missing = (%+v, %v)", missing, err)
	}
	if fake.group != "q" || fake.name != "alarmd:progress" {
		t.Fatalf("Progress control location = (%q, %q)", fake.group, fake.name)
	}
	fake.value = mustEncode(t, execution.ScheduleProgress{Identity: identity, NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull})
	fake.missing = false
	found, err := store.LoadProgress(context.Background(), identity)
	if err != nil || found.Status != execution.ProgressFound || found.Progress.NextSlot != 120 {
		t.Fatalf("found = (%+v, %v)", found, err)
	}
}

func progressContract() execution.FrozenExecutionContractRef {
	return progressContractAt(60)
}

func progressContractAt(evaluationTime execution.EvaluationTime) execution.FrozenExecutionContractRef {
	return execution.FrozenExecutionContractRef{
		Slot: execution.SlotIdentity{QueryGroup: "q", EvaluationTime: evaluationTime}, SnapshotRevision: "s", QueryRevision: "query",
		ScheduleRevision: "r", ScheduleSegmentStart: 60, DuePlanSetDigest: "plans",
	}
}

func mustStore(t *testing.T, control ControlStore) *Store {
	t.Helper()
	return mustStoreWithSlots(t, control, fixedSlotResolver{interval: 60})
}

func mustStoreWithSlots(t *testing.T, control ControlStore, slots ContinuousSlotResolver) *Store {
	t.Helper()
	store, err := NewStore(StoreOptions{
		Prefix: "alarmd", Control: control, Slots: slots,
		Now: func() time.Time { return time.Unix(1, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

type mappedSlotResolver map[execution.EvaluationTime]execution.EvaluationTime

func (resolver mappedSlotResolver) NextSlotAfter(
	_ context.Context,
	_ execution.QueryGroupIdentity,
	completed execution.EvaluationTime,
) (execution.EvaluationTime, error) {
	return resolver[completed], nil
}

type fixedSlotResolver struct {
	interval execution.EvaluationTime
}

func (resolver fixedSlotResolver) NextSlotAfter(
	_ context.Context,
	_ execution.QueryGroupIdentity,
	completed execution.EvaluationTime,
) (execution.EvaluationTime, error) {
	return completed + resolver.interval, nil
}

func mustEncode(t *testing.T, value execution.ScheduleProgress) []byte {
	t.Helper()
	raw, err := encode(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
