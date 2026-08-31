// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package progress

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
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

func TestCommitProgressAdvancesColdStartHistoryWarmingUntilFull(t *testing.T) {
	fake := &controlFake{missing: true}
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}

	commitWarming := func(store *Store, slot execution.EvaluationTime) {
		t.Helper()
		result, err := store.CommitProgress(context.Background(), execution.ProgressCommitRequest{
			Identity: identity, OwnerFence: fence, ExpectedNextSlot: slot,
			Completion: execution.SlotCompletion{
				Contract: progressContractAt(slot), Kind: execution.CompletionPartialGap,
				Primary: &execution.PrimaryInputFact{
					Completeness: execution.CompletenessFull,
					DataState:    execution.DataStateData,
				},
				Result:     observability.ResultDegraded,
				ReasonCode: execution.ReasonCode(contract.ReasonHistoryWarming),
			},
		})
		if err != nil || result.Status != execution.ProgressCommitted {
			t.Fatalf("CommitProgress(warming %d) = (%+v, %v)", slot, result, err)
		}
	}

	first := mustStore(t, fake)
	commitWarming(first, 60)
	loaded, err := first.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil {
		t.Fatalf("LoadProgress(first warming) = (%+v, %v)", loaded, err)
	}
	assertWarmingProgress(t, *loaded.Progress, 120, 60, 60, 1)

	// Re-open the Store over the same persisted value to prove a process restart
	// continues from the next Slot instead of replaying the first warming Slot.
	restarted := mustStore(t, fake)
	commitWarming(restarted, 120)
	loaded, err = restarted.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil {
		t.Fatalf("LoadProgress(second warming) = (%+v, %v)", loaded, err)
	}
	assertWarmingProgress(t, *loaded.Progress, 180, 60, 120, 2)

	result, err := restarted.CommitProgress(context.Background(), execution.ProgressCommitRequest{
		Identity: identity, OwnerFence: fence, ExpectedNextSlot: 180,
		Completion: execution.SlotCompletion{
			Contract: progressContractAt(180), Kind: execution.CompletionFull,
			Primary: &execution.PrimaryInputFact{
				Completeness: execution.CompletenessFull,
				DataState:    execution.DataStateData,
			},
			Result: observability.ResultSuccess,
		},
	})
	if err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress(full) = (%+v, %v)", result, err)
	}
	loaded, err = restarted.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil || loaded.Progress.NextSlot != 240 ||
		loaded.Progress.LastFullSlot != 180 || loaded.Progress.LastCompletionKind != execution.CompletionFull {
		t.Fatalf("LoadProgress(full) = (%+v, %v)", loaded, err)
	}
	assertWarmingGap(t, loaded.Progress.CurrentOrRecentGap, 60, 120, 2)
}

func TestCommitProgressAdvancesAndRestoresConsecutiveGapSkippedSlots(t *testing.T) {
	fake := &controlFake{missing: true}
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}

	commitGapSkipped := func(store *Store, slot execution.EvaluationTime) {
		t.Helper()
		result, err := store.CommitProgress(context.Background(), execution.ProgressCommitRequest{
			Identity: identity, OwnerFence: fence, ExpectedNextSlot: slot,
			Completion: execution.SlotCompletion{
				Contract: progressContractAt(slot), Kind: execution.CompletionGapSkipped,
				Result:     observability.ResultDegraded,
				ReasonCode: execution.ReasonCode(contract.ReasonGapSkipped),
			},
		})
		if err != nil || result.Status != execution.ProgressCommitted {
			t.Fatalf("CommitProgress(gap skipped %d) = (%+v, %v)", slot, result, err)
		}
	}

	first := mustStore(t, fake)
	commitGapSkipped(first, 60)
	loaded, err := first.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil {
		t.Fatalf("LoadProgress(first gap skipped) = (%+v, %v)", loaded, err)
	}
	assertGapSkippedProgress(t, *loaded.Progress, 120, 60, 60, 1)

	// Re-open over the persisted value to prove startup can continue past an
	// expired Slot without replaying it or requiring an in-memory cursor.
	restarted := mustStore(t, fake)
	commitGapSkipped(restarted, 120)
	loaded, err = restarted.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil {
		t.Fatalf("LoadProgress(second gap skipped) = (%+v, %v)", loaded, err)
	}
	assertGapSkippedProgress(t, *loaded.Progress, 180, 60, 120, 2)
}

func TestCommitProgressAdvancesFullDataGuardedByPreviousGapSkipped(t *testing.T) {
	fake := &controlFake{missing: true}
	store := mustStore(t, fake)
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}

	result, err := store.CommitProgress(context.Background(), execution.ProgressCommitRequest{
		Identity: identity, OwnerFence: fence, ExpectedNextSlot: 60,
		Completion: execution.SlotCompletion{
			Contract: progressContractAt(60), Kind: execution.CompletionGapSkipped,
			Result:     observability.ResultDegraded,
			ReasonCode: execution.ReasonCode(contract.ReasonGapSkipped),
		},
	})
	if err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress(gap skipped) = (%+v, %v)", result, err)
	}

	// A following live Slot can have FULL+DATA input while its Level outcome is
	// still guarded by the durable GAP_SKIPPED marker. Completion derivation
	// intentionally preserves that as COMPLETED_WITH_UNAVAILABLE; G1 may advance
	// only this exact guarded combination, not a query/readiness UNAVAILABLE.
	result, err = store.CommitProgress(context.Background(), execution.ProgressCommitRequest{
		Identity: identity, OwnerFence: fence, ExpectedNextSlot: 120,
		Completion: execution.SlotCompletion{
			Contract: progressContractAt(120), Kind: execution.CompletionUnavailable,
			Primary: &execution.PrimaryInputFact{
				Completeness: execution.CompletenessFull,
				DataState:    execution.DataStateData,
			},
			Result:     observability.ResultDegraded,
			ReasonCode: execution.ReasonCode(contract.ReasonGapSkipped),
		},
	})
	if err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress(full data guarded by gap skipped) = (%+v, %v)", result, err)
	}

	loaded, err := store.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil {
		t.Fatalf("LoadProgress() = (%+v, %v)", loaded, err)
	}
	progress := *loaded.Progress
	if progress.NextSlot != 180 || progress.LastFullSlot != 120 ||
		progress.LastCompletionKind != execution.CompletionUnavailable {
		t.Fatalf("guarded full-data Progress = %+v", progress)
	}
	// The guarded live Slot must not rewrite the earlier query-free gap episode.
	gap := progress.CurrentOrRecentGap
	if gap == nil || gap.Kind != execution.CompletionGapSkipped ||
		gap.ReasonCode != execution.ReasonCode(contract.ReasonGapSkipped) ||
		gap.FirstSlot != 60 || gap.LastSlot != 60 || gap.Count != 1 || gap.NextProbeAt != nil {
		t.Fatalf("preserved gap-skipped summary = %+v", gap)
	}
}

func TestCommitProgressDoesNotOpenOtherDegradedCompletionsInG1(t *testing.T) {
	fullData := &execution.PrimaryInputFact{
		Completeness: execution.CompletenessFull,
		DataState:    execution.DataStateData,
	}
	partialData := &execution.PrimaryInputFact{
		Completeness: execution.CompletenessPartial,
		DataState:    execution.DataStateData,
	}
	fullEmpty := &execution.PrimaryInputFact{
		Completeness: execution.CompletenessFull,
		DataState:    execution.DataStateEmpty,
	}
	unavailable := &execution.PrimaryInputFact{
		Completeness: execution.CompletenessUnavailable,
		DataState:    execution.DataStateUnknown,
	}
	for _, test := range []struct {
		name       string
		kind       execution.CompletionKind
		primary    *execution.PrimaryInputFact
		result     observability.Result
		reasonCode execution.ReasonCode
	}{
		{
			name: "other partial reason", kind: execution.CompletionPartialGap, primary: fullData,
			result: observability.ResultDegraded, reasonCode: execution.ReasonCode(contract.ReasonQueryPartial),
		},
		{
			name: "partial primary", kind: execution.CompletionPartialGap, primary: partialData,
			result: observability.ResultDegraded, reasonCode: execution.ReasonCode(contract.ReasonHistoryWarming),
		},
		{
			name: "unavailable", kind: execution.CompletionUnavailable, primary: unavailable,
			result: observability.ResultDegraded, reasonCode: execution.ReasonCode(contract.ReasonQueryUnavailable),
		},
		{
			name: "unavailable other coverage reason", kind: execution.CompletionUnavailable, primary: fullData,
			result: observability.ResultDegraded, reasonCode: execution.ReasonCode(contract.ReasonHistoryWarming),
		},
		{
			name: "unavailable partial primary", kind: execution.CompletionUnavailable, primary: partialData,
			result: observability.ResultDegraded, reasonCode: execution.ReasonCode(contract.ReasonGapSkipped),
		},
		{
			name: "unavailable empty primary", kind: execution.CompletionUnavailable, primary: fullEmpty,
			result: observability.ResultDegraded, reasonCode: execution.ReasonCode(contract.ReasonGapSkipped),
		},
		{
			name: "terminal", kind: execution.CompletionTerminal, primary: fullData,
			result: observability.ResultTerminal, reasonCode: execution.ReasonCode(contract.ReasonRecordInvalid),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &controlFake{missing: true}
			store := mustStore(t, fake)
			request := execution.ProgressCommitRequest{
				Identity: execution.ProgressIdentity{QueryGroup: "q"},
				OwnerFence: execution.OwnerFence{
					QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease",
				},
				ExpectedNextSlot: 60,
				Completion: execution.SlotCompletion{
					Contract: progressContract(), Kind: test.kind, Primary: test.primary,
					Result: test.result, ReasonCode: test.reasonCode,
				},
			}
			if err := request.Validate(); err != nil {
				t.Fatalf("test completion is not valid before the G1 boundary: %v", err)
			}
			_, err := store.CommitProgress(context.Background(), request)
			if err == nil {
				t.Fatalf("CommitProgress(%s) accepted a completion outside the G1 warming boundary", test.name)
			}
			if !fake.missing {
				t.Fatalf("CommitProgress(%s) persisted rejected Progress", test.name)
			}
		})
	}
}

func assertWarmingProgress(
	t *testing.T,
	progress execution.ScheduleProgress,
	nextSlot, firstGap, lastGap execution.EvaluationTime,
	count uint32,
) {
	t.Helper()
	if progress.NextSlot != nextSlot || progress.LastFullSlot != lastGap ||
		progress.LastCompletionKind != execution.CompletionPartialGap {
		t.Fatalf("warming Progress = %+v", progress)
	}
	assertWarmingGap(t, progress.CurrentOrRecentGap, firstGap, lastGap, count)
}

func assertWarmingGap(
	t *testing.T,
	gap *execution.ProgressGapSummary,
	first, last execution.EvaluationTime,
	count uint32,
) {
	t.Helper()
	if gap == nil || gap.Kind != execution.CompletionPartialGap ||
		gap.ReasonCode != execution.ReasonCode(contract.ReasonHistoryWarming) ||
		gap.FirstSlot != first || gap.LastSlot != last || gap.Count != count || gap.NextProbeAt != nil {
		t.Fatalf("warming gap = %+v", gap)
	}
}

func assertGapSkippedProgress(
	t *testing.T,
	progress execution.ScheduleProgress,
	nextSlot, firstGap, lastGap execution.EvaluationTime,
	count uint32,
) {
	t.Helper()
	if progress.NextSlot != nextSlot || progress.LastFullSlot != 0 ||
		progress.LastCompletionKind != execution.CompletionGapSkipped {
		t.Fatalf("gap-skipped Progress = %+v", progress)
	}
	gap := progress.CurrentOrRecentGap
	if gap == nil || gap.Kind != execution.CompletionGapSkipped ||
		gap.ReasonCode != execution.ReasonCode(contract.ReasonGapSkipped) ||
		gap.FirstSlot != firstGap || gap.LastSlot != lastGap || gap.Count != count || gap.NextProbeAt != nil {
		t.Fatalf("gap-skipped summary = %+v", gap)
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
