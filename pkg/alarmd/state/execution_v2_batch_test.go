// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// pipelineMemoryBackend mirrors compareAndSetByDigestScript in memory so the
// store's batching, classification and fallback logic is testable without
// Redis. Counters record how many storage round trips each path costs.
type pipelineMemoryBackend struct {
	casMemoryBackend
	mgetCalls    int
	pipelines    int
	pipelineKeys int
	casCalls     int
	staleOwner   bool
	failPipeline error
	guards       []*FenceGuard
}

func newPipelineMemoryBackend() *pipelineMemoryBackend {
	return &pipelineMemoryBackend{casMemoryBackend: casMemoryBackend{values: make(map[string][]byte)}}
}

func (backend *pipelineMemoryBackend) MGet(ctx context.Context, keys []string) ([][]byte, error) {
	backend.mgetCalls++
	values, err := backend.casMemoryBackend.MGet(ctx, keys)
	for index, key := range keys {
		if _, found := backend.values[key]; !found {
			values[index] = nil
		}
	}
	return values, err
}

func (backend *pipelineMemoryBackend) CompareAndSet(ctx context.Context, key string, expected []byte, missing bool, value []byte, ttl time.Duration) (bool, error) {
	backend.casCalls++
	return backend.casMemoryBackend.CompareAndSet(ctx, key, expected, missing, value, ttl)
}

func (backend *pipelineMemoryBackend) CompareAndSetManyByDigest(_ context.Context, guard *FenceGuard, writes []FencedWrite) ([]FencedWriteOutcome, error) {
	backend.pipelines++
	backend.pipelineKeys += len(writes)
	backend.guards = append(backend.guards, guard)
	if backend.failPipeline != nil {
		return nil, backend.failPipeline
	}
	outcomes := make([]FencedWriteOutcome, len(writes))
	for index, write := range writes {
		if guard != nil && backend.staleOwner {
			outcomes[index] = FencedWriteOutcome{Status: FencedWriteStaleOwner}
			continue
		}
		current, found := backend.values[write.Key]
		switch {
		case write.ExpectedMissing && found:
			outcomes[index] = FencedWriteOutcome{Status: FencedWriteConflict, Current: append([]byte(nil), current...)}
		case !write.ExpectedMissing && !found:
			outcomes[index] = FencedWriteOutcome{Status: FencedWriteConflictMissing}
		case !write.ExpectedMissing && ExpectedValueDigest(current) != write.ExpectedDigest:
			outcomes[index] = FencedWriteOutcome{Status: FencedWriteConflict, Current: append([]byte(nil), current...)}
		default:
			backend.values[write.Key] = append([]byte(nil), write.Value...)
			outcomes[index] = FencedWriteOutcome{Status: FencedWriteApplied}
		}
	}
	return outcomes, nil
}

type fixedFenceKeys struct{ keys ownership.FenceKeys }

func (resolver fixedFenceKeys) FenceKeys(execution.QueryGroupIdentity) ownership.FenceKeys {
	return resolver.keys
}

func testFenceKeys() ownership.FenceKeys {
	return ownership.FenceKeys{AssignmentKey: "own:{q}:assignment", OwnershipKey: "own:{q}:ownership", RequireAssignment: true}
}

func testApplyFence() execution.StateApplyFence {
	return execution.StateApplyFence{
		Fence: execution.OwnerFence{QueryGroup: frozenRef().Slot.QueryGroup, OwnerID: "worker-1", OwnerEpoch: 3, LeaseToken: "lease-token"},
		At:    time.Unix(1_700_000_000, 0),
	}
}

func seriesIdentity(index int) execution.StateKeyIdentity {
	identity := stateIdentityV2()
	identity.SeriesIdentityDigest = execution.SeriesIdentityDigest(fmt.Sprintf("series-%05d", index))
	return identity
}

func seriesMutation(t *testing.T, identity execution.StateKeyIdentity, version execution.ApplyVersion, revision uint64, padding string) execution.StateMutation {
	t.Helper()
	at := int64(version.EvaluationTime)
	mutation, err := execution.BuildStateMutation(execution.StateMutation{Identity: identity, ExpectedBlobRevision: revision, ApplyVersion: version,
		AffectedRecords: []execution.RecordAnchor{{RecordID: "r1", SourceTime: at}},
		Levels: []execution.RuntimeLevelStateMutation{{LevelID: 1, LevelStateCompatibility: "compat", HistoryCompleteness: execution.HistoryFull,
			WarmupRequirementRef: "warm" + padding, LastProcessedEventTime: at}},
		Points: []execution.StateHistoryPoint{{RecordID: "r1", SourceTime: at,
			Levels: []execution.StateLevelFact{{LevelID: 1, DetectFingerprint: "detect", Result: execution.LevelFactNormal}}}}})
	if err != nil {
		t.Fatal(err)
	}
	return mutation
}

func seriesMutations(t *testing.T, count int, version execution.ApplyVersion, revision uint64) []execution.StateMutation {
	t.Helper()
	mutations := make([]execution.StateMutation, count)
	for index := range mutations {
		mutations[index] = seriesMutation(t, seriesIdentity(index), version, revision, "")
	}
	return mutations
}

func preflightItems(mutations []execution.StateMutation) []execution.StatePreflightItem {
	items := make([]execution.StatePreflightItem, len(mutations))
	for index, mutation := range mutations {
		items[index] = execution.StatePreflightItem{Identity: mutation.Identity, ApplyVersion: mutation.ApplyVersion}
	}
	return items
}

func newBatchStore(t *testing.T, backend Backend, resolver FenceKeyResolver) *ExecutionStore {
	t.Helper()
	router, err := NewFixedRouter("monitor-01", backend)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewExecutionStore(ExecutionStoreOptions{Prefix: "alarmd", Router: router, MaxValueBytes: 1 << 20,
		MaxItemsPerCall: 8192, MinTTL: time.Minute, MaxTTL: time.Hour, RestartMargin: time.Minute, FenceKeys: resolver})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func requireAllStatus(t *testing.T, result execution.StateApplyResult, want execution.StateApplyStatus) {
	t.Helper()
	for index, item := range result.Items {
		if item.Status != want {
			t.Fatalf("item %d status = %s (%s), want %s", index, item.Status, item.ReasonCode, want)
		}
	}
}

func TestLoadRuntimeBatchesReadsAndIsolatesInvalidItems(t *testing.T) {
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, nil)
	mutations := seriesMutations(t, 600, applyVersion(), 0)
	validKey, _ := RuntimeStateKeyV2("alarmd", mutations[10].Identity)
	backend.values[validKey], _ = encodeRuntime(mutations[10], 1)
	corruptKey, _ := RuntimeStateKeyV2("alarmd", mutations[300].Identity)
	backend.values[corruptKey] = []byte("not-json")
	oversizeKey, _ := RuntimeStateKeyV2("alarmd", mutations[599].Identity)
	backend.values[oversizeKey] = []byte(strings.Repeat("x", (1<<20)+1))

	loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: preflightItems(mutations)})
	if err != nil {
		t.Fatalf("LoadRuntime() error = %v", err)
	}
	if backend.mgetCalls != 3 {
		t.Fatalf("MGET round trips = %d, want ceil(600/256) = 3", backend.mgetCalls)
	}
	for index, view := range loaded.Items {
		want := execution.StateMissingWarming
		switch index {
		case 10:
			want = execution.StateFoundReady
		case 300, 599:
			want = execution.StateDeterministicInvalid
		}
		if view.Status != want || view.Identity != mutations[index].Identity {
			t.Fatalf("item %d = %+v, want %s", index, view, want)
		}
	}
	if loaded.Items[300].ReasonCode != execution.ReasonCode(contract.ReasonStateCorrupt) ||
		loaded.Items[599].ReasonCode != execution.ReasonCode(contract.ReasonStateBudgetExceeded) {
		t.Fatalf("invalid reasons = %s / %s", loaded.Items[300].ReasonCode, loaded.Items[599].ReasonCode)
	}
	slot := frozenRef().Slot
	if witness, ok := store.witnesses.take(slot, mutations[10].Identity); !ok || witness.missing || witness.blobRevision != 1 ||
		witness.digest != ExpectedValueDigest(backend.values[validKey]) {
		t.Fatalf("found witness = (%+v, %t)", witness, ok)
	}
	if witness, ok := store.witnesses.take(slot, mutations[0].Identity); !ok || !witness.missing {
		t.Fatalf("missing witness = (%+v, %t)", witness, ok)
	}
	for _, index := range []int{300, 599} {
		if _, ok := store.witnesses.take(slot, mutations[index].Identity); ok {
			t.Fatalf("invalid item %d must not leave a witness", index)
		}
	}
}

func TestLoadRuntimeFailedBatchIsRetryableAndLeavesNoWitness(t *testing.T) {
	backend := newFakeBackend()
	backend.readErr = errors.New("connection reset")
	store := newBatchStore(t, backend, nil)
	mutations := seriesMutations(t, 3, applyVersion(), 0)
	loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: preflightItems(mutations)})
	if err != nil {
		t.Fatalf("LoadRuntime() error = %v", err)
	}
	for index, view := range loaded.Items {
		if view.Status != execution.StateRetryableIO || view.ReasonCode != execution.ReasonCode(contract.ReasonRedisUnavailable) {
			t.Fatalf("item %d = %+v, want retryable IO", index, view)
		}
		if _, ok := store.witnesses.take(frozenRef().Slot, mutations[index].Identity); ok {
			t.Fatalf("failed read left a witness for item %d", index)
		}
	}
}

func TestApplyRuntimePipelinesWitnessedItemsAndStoresSequentialBytes(t *testing.T) {
	batched := newPipelineMemoryBackend()
	batchedStore := newBatchStore(t, batched, fixedFenceKeys{testFenceKeys()})
	sequential := &casMemoryBackend{values: make(map[string][]byte)}
	sequentialStore := newBatchStore(t, sequential, nil)
	mutations := seriesMutations(t, 1000, applyVersion(), 0)
	request := execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(), Items: mutations}

	if _, err := batchedStore.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: preflightItems(mutations)}); err != nil {
		t.Fatalf("LoadRuntime() error = %v", err)
	}
	if batched.mgetCalls != 4 {
		t.Fatalf("preflight MGET round trips = %d, want ceil(1000/256) = 4", batched.mgetCalls)
	}
	result, err := batchedStore.ApplyRuntimeFenced(context.Background(), request, testApplyFence())
	if err != nil {
		t.Fatalf("ApplyRuntimeFenced() error = %v", err)
	}
	requireAllStatus(t, result, execution.StateApplied)
	if batched.pipelines != 4 || batched.pipelineKeys != 1000 || batched.casCalls != 0 || batched.mgetCalls != 4 {
		t.Fatalf("apply round trips: pipelines=%d keys=%d cas=%d mget=%d, want 4 pipelines carrying 1000 keys and no re-read",
			batched.pipelines, batched.pipelineKeys, batched.casCalls, batched.mgetCalls)
	}
	for _, guard := range batched.guards {
		if guard == nil || guard.Keys != testFenceKeys() || guard.OwnerID != "worker-1" || guard.OwnerEpoch != 3 ||
			guard.LeaseToken != "lease-token" || guard.NowMillis != testApplyFence().At.UnixMilli() {
			t.Fatalf("pipeline guard = %+v", guard)
		}
	}

	sequentialResult, err := sequentialStore.ApplyRuntime(context.Background(), request)
	if err != nil {
		t.Fatalf("sequential ApplyRuntime() error = %v", err)
	}
	requireAllStatus(t, sequentialResult, execution.StateApplied)
	if len(sequential.values) != 1000 || len(batched.values) != 1000 {
		t.Fatalf("stored keys = %d / %d", len(sequential.values), len(batched.values))
	}
	for key, want := range sequential.values {
		if got, found := batched.values[key]; !found || string(got) != string(want) {
			t.Fatalf("stored bytes differ for %s", key)
		}
	}

	// Replay after a crash between State apply and Progress commit: the next
	// attempt preflights again and the identical mutation is already applied
	// without any write.
	for index := range mutations {
		mutations[index].ExpectedBlobRevision = 1
	}
	if _, err := batchedStore.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: preflightItems(mutations)}); err != nil {
		t.Fatalf("replay LoadRuntime() error = %v", err)
	}
	before := batched.pipelines
	replay, err := batchedStore.ApplyRuntimeFenced(context.Background(), request, testApplyFence())
	if err != nil {
		t.Fatalf("replay ApplyRuntimeFenced() error = %v", err)
	}
	requireAllStatus(t, replay, execution.StateApplyAlreadyApplied)
	if batched.pipelines != before || batched.casCalls != 0 {
		t.Fatalf("already-applied replay reached storage: pipelines=%d cas=%d", batched.pipelines-before, batched.casCalls)
	}
}

func TestApplyRuntimeWithoutWitnessKeepsSequentialPath(t *testing.T) {
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, fixedFenceKeys{testFenceKeys()})
	mutations := seriesMutations(t, 5, applyVersion(), 0)
	result, err := store.ApplyRuntimeFenced(context.Background(), execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(), Items: mutations}, testApplyFence())
	if err != nil {
		t.Fatalf("ApplyRuntimeFenced() error = %v", err)
	}
	requireAllStatus(t, result, execution.StateApplied)
	if backend.pipelines != 0 || backend.casCalls != 5 || backend.mgetCalls != 5 {
		t.Fatalf("legacy caller round trips: pipelines=%d cas=%d mget=%d", backend.pipelines, backend.casCalls, backend.mgetCalls)
	}
}

func TestApplyRuntimeClassifiesValueChangedBetweenPreflightAndApply(t *testing.T) {
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, nil)
	version := applyVersion()
	newer := execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 120, SlotDigest: "slot-2"}
	older := execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 30, SlotDigest: "slot-0"}
	missingThenWritten := seriesMutation(t, seriesIdentity(0), version, 0, "")
	movedRevision := seriesMutation(t, seriesIdentity(1), newer, 1, "")
	movedBytes := seriesMutation(t, seriesIdentity(2), newer, 1, "")
	vanished := seriesMutation(t, seriesIdentity(3), newer, 1, "")
	for _, mutation := range []execution.StateMutation{movedRevision, movedBytes, vanished} {
		key, _ := RuntimeStateKeyV2("alarmd", mutation.Identity)
		backend.values[key], _ = encodeRuntime(seriesMutation(t, mutation.Identity, version, 0, ""), 1)
	}
	items := preflightItems([]execution.StateMutation{missingThenWritten, movedRevision, movedBytes, vanished})
	if _, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: items}); err != nil {
		t.Fatalf("LoadRuntime() error = %v", err)
	}

	// Another writer moves every key after preflight.
	key0, _ := RuntimeStateKeyV2("alarmd", missingThenWritten.Identity)
	backend.values[key0], _ = encodeRuntime(seriesMutation(t, missingThenWritten.Identity, version, 0, "other"), 1)
	key1, _ := RuntimeStateKeyV2("alarmd", movedRevision.Identity)
	backend.values[key1], _ = encodeRuntime(seriesMutation(t, movedRevision.Identity, version, 1, ""), 2)
	key2, _ := RuntimeStateKeyV2("alarmd", movedBytes.Identity)
	backend.values[key2], _ = encodeRuntime(seriesMutation(t, movedBytes.Identity, older, 0, "other"), 1)
	key3, _ := RuntimeStateKeyV2("alarmd", vanished.Identity)
	delete(backend.values, key3)
	snapshot := map[string]string{}
	for key, value := range backend.values {
		snapshot[key] = string(value)
	}

	result, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(),
		Items: []execution.StateMutation{missingThenWritten, movedRevision, movedBytes, vanished}})
	if err != nil {
		t.Fatalf("ApplyRuntime() error = %v", err)
	}
	want := []execution.StateApplyStatus{execution.StateApplyVersionConflict, execution.StateApplyVersionConflict,
		execution.StateApplyCASConflict, execution.StateApplyVersionConflict}
	for index, status := range want {
		if result.Items[index].Status != status {
			t.Fatalf("item %d = %+v, want %s", index, result.Items[index], status)
		}
	}
	if result.Items[2].ReasonCode != execution.ReasonCode(contract.ReasonStateWriteRetryable) {
		t.Fatalf("CAS conflict reason = %s", result.Items[2].ReasonCode)
	}
	if len(backend.values) != len(snapshot) {
		t.Fatalf("conflicting apply changed the key set: %d != %d", len(backend.values), len(snapshot))
	}
	for key, value := range snapshot {
		if string(backend.values[key]) != value {
			t.Fatalf("conflicting apply overwrote %s", key)
		}
	}
	if backend.pipelines != 1 || backend.casCalls != 0 {
		t.Fatalf("conflict path round trips: pipelines=%d cas=%d", backend.pipelines, backend.casCalls)
	}
}

func TestApplyRuntimeRepeatedKeyFallsBackToSequentialPath(t *testing.T) {
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, nil)
	first := seriesMutation(t, seriesIdentity(0), applyVersion(), 0, "")
	sibling := seriesMutation(t, seriesIdentity(1), applyVersion(), 0, "")
	second := seriesMutation(t, seriesIdentity(0), execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 120, SlotDigest: "slot-2"}, 1, "")
	if _, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(),
		Items: preflightItems([]execution.StateMutation{first, sibling})}); err != nil {
		t.Fatalf("LoadRuntime() error = %v", err)
	}
	result, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(),
		Items: []execution.StateMutation{first, sibling, second}})
	if err != nil {
		t.Fatalf("ApplyRuntime() error = %v", err)
	}
	requireAllStatus(t, result, execution.StateApplied)
	if backend.pipelines != 0 || backend.casCalls != 3 {
		t.Fatalf("repeated key must use the sequential path: pipelines=%d cas=%d", backend.pipelines, backend.casCalls)
	}
	key, _ := RuntimeStateKeyV2("alarmd", first.Identity)
	view := decodeRuntime(backend.values[key], first.Identity, frozenRef(), second.ApplyVersion)
	if view.BlobRevision != 2 || view.PersistedMutationDigest != second.MutationDigest {
		t.Fatalf("later duplicate did not observe the earlier write: %+v", view)
	}
	if _, ok := store.witnesses.take(frozenRef().Slot, first.Identity); ok {
		t.Fatal("sequential fallback must still consume the witness")
	}
}

func TestApplyRuntimeFencedSurfacesStaleOwnerWithoutWrites(t *testing.T) {
	backend := newPipelineMemoryBackend()
	backend.staleOwner = true
	store := newBatchStore(t, backend, fixedFenceKeys{testFenceKeys()})
	mutations := seriesMutations(t, 3, applyVersion(), 0)
	if _, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: preflightItems(mutations)}); err != nil {
		t.Fatalf("LoadRuntime() error = %v", err)
	}
	result, err := store.ApplyRuntimeFenced(context.Background(), execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(), Items: mutations}, testApplyFence())
	if !errors.Is(err, ownership.ErrStaleFence) || len(result.Items) != 0 {
		t.Fatalf("ApplyRuntimeFenced(stale) = (%+v, %v), want ErrStaleFence", result, err)
	}
	if len(backend.values) != 0 {
		t.Fatal("stale owner wrote Runtime State")
	}

	invalid := testApplyFence()
	invalid.Fence.LeaseToken = ""
	if _, err := store.ApplyRuntimeFenced(context.Background(), execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(), Items: mutations}, invalid); err == nil {
		t.Fatal("invalid fence was accepted")
	}

	// Without a resolver the store cannot locate the lease and applies
	// unfenced, which is the behaviour before this change.
	unfenced := newBatchStore(t, backend, nil)
	if _, err := unfenced.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: preflightItems(mutations)}); err != nil {
		t.Fatal(err)
	}
	applied, err := unfenced.ApplyRuntimeFenced(context.Background(), execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(), Items: mutations}, testApplyFence())
	if err != nil {
		t.Fatalf("unfenced ApplyRuntimeFenced() error = %v", err)
	}
	requireAllStatus(t, applied, execution.StateApplied)
	if backend.guards[len(backend.guards)-1] != nil {
		t.Fatal("store without resolver sent a fence guard")
	}
}

func TestApplyRuntimePipelineFailureMarksWholeBatchRetryable(t *testing.T) {
	backend := newPipelineMemoryBackend()
	backend.failPipeline = errors.New("connection reset")
	store := newBatchStore(t, backend, nil)
	mutations := seriesMutations(t, 300, applyVersion(), 0)
	if _, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: preflightItems(mutations)}); err != nil {
		t.Fatalf("LoadRuntime() error = %v", err)
	}
	result, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(), Items: mutations})
	if err != nil {
		t.Fatalf("ApplyRuntime() error = %v", err)
	}
	requireAllStatus(t, result, execution.StateApplyRetryable)
	for index, item := range result.Items {
		if item.ReasonCode != execution.ReasonCode(contract.ReasonStateWriteRetryable) || item.Identity != mutations[index].Identity {
			t.Fatalf("item %d = %+v", index, item)
		}
	}
	if backend.pipelines != 2 || len(backend.values) != 0 {
		t.Fatalf("failed pipelines=%d stored=%d", backend.pipelines, len(backend.values))
	}
}

func TestApplyRuntimeCutsPipelineBatchesByBytes(t *testing.T) {
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, nil)
	padding := strings.Repeat("p", 96<<10)
	mutations := make([]execution.StateMutation, 100)
	for index := range mutations {
		mutations[index] = seriesMutation(t, seriesIdentity(index), applyVersion(), 0, padding)
	}
	if _, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: preflightItems(mutations)}); err != nil {
		t.Fatalf("LoadRuntime() error = %v", err)
	}
	result, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(), Items: mutations})
	if err != nil {
		t.Fatalf("ApplyRuntime() error = %v", err)
	}
	requireAllStatus(t, result, execution.StateApplied)
	// 100 values of about 96 KiB each exceed the 8 MiB byte bound once.
	if backend.pipelines != 2 {
		t.Fatalf("pipelines = %d, want the byte bound to cut 100 large values into 2 round trips", backend.pipelines)
	}
}

func TestRuntimeWitnessCacheScopesBySlotAndEvictsOldGroups(t *testing.T) {
	cache := newRuntimeWitnessCache()
	identity := seriesIdentity(0)
	first := execution.SlotIdentity{QueryGroup: "q", EvaluationTime: 60}
	second := execution.SlotIdentity{QueryGroup: "q", EvaluationTime: 120}
	cache.remember(first, identity, runtimeWitness{missing: true})
	if _, ok := cache.take(second, identity); ok {
		t.Fatal("witness of an earlier Slot served a later Slot")
	}
	if witness, ok := cache.take(first, identity); !ok || !witness.missing {
		t.Fatal("witness of the same Slot was lost")
	}
	if _, ok := cache.take(first, identity); ok {
		t.Fatal("witness served twice")
	}
	cache.remember(first, identity, runtimeWitness{missing: true})
	cache.remember(second, identity, runtimeWitness{digest: "d"})
	if _, ok := cache.take(first, identity); ok {
		t.Fatal("newer Slot did not replace the earlier witnesses of its query group")
	}
	if cache.total != 1 {
		t.Fatalf("total = %d, want 1", cache.total)
	}

	for index := 0; index < runtimeWitnessMaxGroups+5; index++ {
		slot := execution.SlotIdentity{QueryGroup: execution.QueryGroupIdentity(fmt.Sprintf("group-%d", index)), EvaluationTime: 60}
		cache.remember(slot, identity, runtimeWitness{missing: true})
	}
	if len(cache.groups) != runtimeWitnessMaxGroups || cache.total != runtimeWitnessMaxGroups {
		t.Fatalf("groups=%d total=%d, want bounded at %d", len(cache.groups), cache.total, runtimeWitnessMaxGroups)
	}
	if _, ok := cache.take(second, identity); ok {
		t.Fatal("oldest group survived eviction")
	}
	newest := execution.SlotIdentity{QueryGroup: execution.QueryGroupIdentity(fmt.Sprintf("group-%d", runtimeWitnessMaxGroups+4)), EvaluationTime: 60}
	if _, ok := cache.take(newest, identity); !ok {
		t.Fatal("newest group was evicted")
	}
}
