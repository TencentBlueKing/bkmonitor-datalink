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
	failMGet     error
	guards       []*FenceGuard
	// recordKeyCounts keeps how many keys each MGET carried, for the tests
	// that assert on the batch bound rather than on the number of calls.
	recordKeyCounts bool
	keyCounts       []int
	byteCounts      []int
}

func newPipelineMemoryBackend() *pipelineMemoryBackend {
	return &pipelineMemoryBackend{casMemoryBackend: casMemoryBackend{values: make(map[string][]byte)}}
}

func (backend *pipelineMemoryBackend) MGet(ctx context.Context, keys []string) ([][]byte, error) {
	backend.mgetCalls++
	if backend.recordKeyCounts {
		backend.keyCounts = append(backend.keyCounts, len(keys))
		bytes := 0
		for _, key := range keys {
			bytes += len(backend.values[key])
		}
		backend.byteCounts = append(backend.byteCounts, bytes)
	}
	if backend.failMGet != nil {
		return nil, backend.failMGet
	}
	values, err := backend.casMemoryBackend.MGet(ctx, keys)
	for index, key := range keys {
		if _, found := backend.values[key]; !found {
			values[index] = nil
		}
	}
	return values, err
}

func (backend *pipelineMemoryBackend) RenewIfBelow(
	ctx context.Context, key string, ttl, threshold time.Duration,
) (RenewalOutcome, error) {
	outcomes, err := backend.RenewManyIfBelow(ctx, []string{key}, ttl, threshold)
	if err != nil {
		return "", err
	}
	return outcomes[0], nil
}

func (backend *pipelineMemoryBackend) RenewManyIfBelow(
	_ context.Context, keys []string, ttl, threshold time.Duration,
) ([]RenewalOutcome, error) {
	_, _ = ttl, threshold
	outcomes := make([]RenewalOutcome, len(keys))
	for index, key := range keys {
		outcomes[index] = RenewalMissing
		if _, exists := backend.values[key]; exists {
			outcomes[index] = RenewalRenewed
		}
	}
	return outcomes, nil
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
	}
}

func seriesIdentity(index int) execution.StateKeyIdentity {
	identity := stateIdentityV2()
	identity.SeriesIdentityDigest = seriesDigest(fmt.Sprintf("series-%05d", index))
	return identity
}

func seriesMutation(t *testing.T, identity execution.StateKeyIdentity, version execution.ApplyVersion, revision uint64, padding string) execution.StateMutation {
	t.Helper()
	at := int64(version.EvaluationTime)
	mutation, err := execution.BuildStateMutation(execution.StateMutation{Identity: identity, ExpectedBlobRevision: revision, ApplyVersion: version,
		AffectedRecords: []execution.RecordAnchor{derivedAnchor(t, identity, at)},
		Levels: []execution.RuntimeLevelStateMutation{{LevelID: 1, LevelStateCompatibility: "compat", HistoryCompleteness: execution.HistoryFull,
			WarmupRequirementRef: "warm" + padding, LastProcessedEventTime: at}},
		Points: []execution.StateHistoryPoint{derivedPoint(t, identity, at, "detect", execution.LevelFactNormal)}})
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
	router, err := NewFixedRouter("state-01", backend)
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
	// One safe batch, then the item bound. A Query Group nothing has been read
	// for yet is bounded by the batch budget over the largest value the store
	// accepts, because that is the only bound that holds whatever its records
	// turn out to be; the first batch teaches their real size and the rest run
	// at the item bound. The extra round trip is that one call, per Query
	// Group, and it is what stops a Query Group whose records grew to 345 KiB
	// from asking for 86 MB in one MGET.
	//
	// Twice over here, because every series in this fixture holds an envelope
	// and no frame: the first pass reads 600 frames and finds none, the second
	// reads the 600 envelopes that answer. A fleet that has finished the
	// migration pays the first pass only - which is the point of the split -
	// and this fixture is what the middle of the migration costs.
	// The envelope pass runs at the value bound throughout - eight keys per
	// call in this fixture, sixteen under the production limit - because a
	// bound learned from its own earlier batches is the trap the sibling
	// bound is protected from by a committed measurement this pass does not
	// have. Empty replies are what those calls cost while the migration is
	// unfinished, and the pass disappears when the older keys do.
	if backend.mgetCalls != 79 {
		t.Fatalf("MGET round trips = %d, want the frame pass (1 safe batch of 16 + ceil(584/256) = 4) and the envelope pass (600 / 8 = 75)", backend.mgetCalls)
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
	// The read-only fake would not pass the probe at open; the transport
	// failure under test is reached through a router that listed a capable
	// target and routes to this one.
	store := capabilityStore(t, backend)
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
	// One safe batch, then the item bound. A Query Group nothing has been read
	// for yet is bounded by the batch budget over the largest value the store
	// accepts, because that is the only bound that holds whatever its records
	// turn out to be; the first batch teaches their real size and the rest run
	// at the item bound. The extra round trip is that one call, per Query
	// Group, and it is what stops a Query Group whose records grew to 345 KiB
	// from asking for 86 MB in one MGET.
	// Plus the envelope pass: every series here is missing from both keys, so
	// the frame pass answers none of them and the second asks the older key.
	// Its first call is bounded by what the store accepts as a value, because
	// nothing has measured an envelope yet; that call comes back empty, which
	// is a measurement of this representation, and the rest run at the item
	// bound. A missing key costs a reply and no bytes, which is what makes
	// paying it for a cold Query Group acceptable; what it buys is never
	// reading a 345 KiB envelope for a series whose frame answers.
	if batched.mgetCalls != 130 {
		t.Fatalf("preflight MGET round trips = %d, want the frame pass (5) and the envelope pass at the value bound (1000 / 8 = 125)", batched.mgetCalls)
	}
	result, err := batchedStore.ApplyRuntimeFenced(context.Background(), request, testApplyFence())
	if err != nil {
		t.Fatalf("ApplyRuntimeFenced() error = %v", err)
	}
	requireAllStatus(t, result, execution.StateApplied)
	// The MGETs are the preflight's 130 above - its frame pass and its
	// envelope pass; the apply itself re-reads nothing, which is what this
	// counts.
	if batched.pipelines != 4 || batched.pipelineKeys != 1000 || batched.casCalls != 0 || batched.mgetCalls != 130 {
		t.Fatalf("apply round trips: pipelines=%d keys=%d cas=%d mget=%d, want 4 pipelines carrying 1000 keys and no re-read",
			batched.pipelines, batched.pipelineKeys, batched.casCalls, batched.mgetCalls)
	}
	for _, guard := range batched.guards {
		if guard == nil || guard.Keys != testFenceKeys() || guard.OwnerID != "worker-1" || guard.OwnerEpoch != 3 ||
			guard.LeaseToken != "lease-token" {
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
	recreated := seriesMutation(t, seriesIdentity(4), newer, 3, "")
	for _, mutation := range []execution.StateMutation{movedRevision, movedBytes, vanished} {
		key, _ := RuntimeStateKeyV3("alarmd", mutation.Identity)
		backend.values[key], _ = encodeRuntimePacked(seriesMutation(t, mutation.Identity, version, 0, ""), 1)
	}
	key4, _ := RuntimeStateKeyV3("alarmd", recreated.Identity)
	backend.values[key4], _ = encodeRuntimePacked(seriesMutation(t, recreated.Identity, version, 2, ""), 3)
	items := preflightItems([]execution.StateMutation{missingThenWritten, movedRevision, movedBytes, vanished, recreated})
	if _, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: items}); err != nil {
		t.Fatalf("LoadRuntime() error = %v", err)
	}

	// Another writer moves every key after preflight.
	key0, _ := RuntimeStateKeyV3("alarmd", missingThenWritten.Identity)
	backend.values[key0], _ = encodeRuntimePacked(seriesMutation(t, missingThenWritten.Identity, version, 0, "other"), 1)
	key1, _ := RuntimeStateKeyV3("alarmd", movedRevision.Identity)
	backend.values[key1], _ = encodeRuntimePacked(seriesMutation(t, movedRevision.Identity, version, 1, ""), 2)
	key2, _ := RuntimeStateKeyV3("alarmd", movedBytes.Identity)
	backend.values[key2], _ = encodeRuntimePacked(seriesMutation(t, movedBytes.Identity, older, 0, "other"), 1)
	key3, _ := RuntimeStateKeyV3("alarmd", vanished.Identity)
	delete(backend.values, key3)
	// The key was gone and written fresh: revision 3 at preflight, 1 now.
	backend.values[key4], _ = encodeRuntimePacked(seriesMutation(t, recreated.Identity, version, 0, "other"), 1)
	snapshot := map[string]string{}
	for key, value := range backend.values {
		snapshot[key] = string(value)
	}

	result, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(),
		Items: []execution.StateMutation{missingThenWritten, movedRevision, movedBytes, vanished, recreated}})
	if err != nil {
		t.Fatalf("ApplyRuntime() error = %v", err)
	}
	want := []execution.StateApplyStatus{execution.StateApplyVersionConflict, execution.StateApplyVersionConflict,
		execution.StateApplyCASConflict, execution.StateApplyVersionConflict, execution.StateApplyVersionConflict}
	for index, status := range want {
		if result.Items[index].Status != status {
			t.Fatalf("item %d = %+v, want %s", index, result.Items[index], status)
		}
	}
	if result.Items[2].ReasonCode != execution.ReasonCode(contract.ReasonStateWriteRetryable) {
		t.Fatalf("CAS conflict reason = %s", result.Items[2].ReasonCode)
	}
	// Each conflict says which comparison refused it and what it compared;
	// the status alone reads the same for a key that expired and a key
	// another writer moved.
	wantKinds := map[int]execution.StateApplyItemResult{
		0: {VersionConflict: execution.StateVersionConflictRevisionMoved, StoredBlobRevision: 1, StoredVersionComparison: execution.ApplyVersionEqual},
		1: {VersionConflict: execution.StateVersionConflictRevisionMoved, StoredBlobRevision: 2, StoredVersionComparison: execution.ApplyVersionPersistedOlder},
		3: {VersionConflict: execution.StateVersionConflictMissing},
		4: {VersionConflict: execution.StateVersionConflictRevisionReset, StoredBlobRevision: 1, StoredVersionComparison: execution.ApplyVersionPersistedOlder},
	}
	for index, want := range wantKinds {
		got := result.Items[index]
		if got.VersionConflict != want.VersionConflict || got.StoredBlobRevision != want.StoredBlobRevision || got.StoredVersionComparison != want.StoredVersionComparison {
			t.Fatalf("item %d = %+v, want kind %s stored revision %d comparison %q", index, got, want.VersionConflict, want.StoredBlobRevision, want.StoredVersionComparison)
		}
	}
	if result.Items[2].VersionConflict != "" {
		t.Fatalf("CAS conflict carries a version conflict kind: %+v", result.Items[2])
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
	key, _ := RuntimeStateKeyV3("alarmd", first.Identity)
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

// The same write sent a second time, unchanged, is the statement already on
// disk and must read ALREADY_APPLIED with kind revision_skew, without a write.
//
// This is what the Redis client library does on its own: a pipeline whose
// reply was lost to a read timeout or a dropped connection is re-sent as it
// was, and the first copy had already executed. Nothing preflights again in
// between, so the shape is built the way it happens: the witness still says
// the key is missing, the bytes on disk are already ours. Classifying the
// second copy by revision first called our own landed write a conflict, and
// the Slot's retry then re-evaluated against post-Slot state and conflicted
// again on every attempt.
func TestApplyRuntimeTheSameWriteSentAgainUnchangedIsAlreadyApplied(t *testing.T) {
	requireSkew := func(t *testing.T, result execution.StateApplyResult) {
		t.Helper()
		for index, item := range result.Items {
			if item.Status != execution.StateApplyAlreadyApplied {
				t.Fatalf("item %d after an unchanged re-send = %+v, want %s: the bytes on disk are this very "+
					"mutation, and calling them a conflict sends the Slot into a retry that cannot ever succeed",
					index, item, execution.StateApplyAlreadyApplied)
			}
			// The item says how it was decided and where the statement was
			// found, or the coordinator cannot count re-sends apart from
			// ordinary replays.
			if item.AlreadyApplied != execution.StateAlreadyAppliedRevisionSkew || item.StoredBlobRevision != 1 {
				t.Fatalf("item %d after an unchanged re-send = %+v, want kind revision_skew at stored revision 1", index, item)
			}
		}
	}
	requireUntouched := func(t *testing.T, backend *pipelineMemoryBackend, snapshot map[string]string) {
		t.Helper()
		if len(backend.values) != len(snapshot) {
			t.Fatalf("re-send changed the key set: %d != %d", len(backend.values), len(snapshot))
		}
		for key, value := range snapshot {
			if string(backend.values[key]) != value {
				t.Fatalf("re-send rewrote %s", key)
			}
		}
	}
	snapshotOf := func(backend *pipelineMemoryBackend) map[string]string {
		snapshot := map[string]string{}
		for key, value := range backend.values {
			snapshot[key] = string(value)
		}
		return snapshot
	}

	t.Run("pipelined fenced write whose first copy landed", func(t *testing.T) {
		backend := newPipelineMemoryBackend()
		store := newBatchStore(t, backend, fixedFenceKeys{testFenceKeys()})
		mutations := seriesMutations(t, 3, applyVersion(), 0)
		request := execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(), Items: mutations}
		if _, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: preflightItems(mutations)}); err != nil {
			t.Fatalf("LoadRuntime() error = %v", err)
		}
		// The first copy executed: our bytes are on disk at revision 1. The
		// witness from preflight still says missing, so the re-sent copy goes
		// to the pipeline and meets them there.
		for _, mutation := range mutations {
			key, _ := RuntimeStateKeyV3("alarmd", mutation.Identity)
			backend.values[key], _ = encodeRuntimePacked(mutation, 1)
		}
		snapshot := snapshotOf(backend)
		result, err := store.ApplyRuntimeFenced(context.Background(), request, testApplyFence())
		if err != nil {
			t.Fatalf("ApplyRuntimeFenced() error = %v", err)
		}
		if backend.pipelines != 1 {
			t.Fatalf("pipelines = %d, want the re-sent copy to reach the pipeline and be classified from the conflict it meets", backend.pipelines)
		}
		requireSkew(t, result)
		requireUntouched(t, backend, snapshot)
	})

	t.Run("sequential write sent again without a new preflight", func(t *testing.T) {
		backend := newPipelineMemoryBackend()
		store := newBatchStore(t, backend, nil)
		mutations := seriesMutations(t, 3, applyVersion(), 0)
		request := execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(), Items: mutations}
		first, err := store.ApplyRuntime(context.Background(), request)
		if err != nil {
			t.Fatalf("ApplyRuntime() error = %v", err)
		}
		requireAllStatus(t, first, execution.StateApplied)
		snapshot := snapshotOf(backend)
		again, err := store.ApplyRuntime(context.Background(), request)
		if err != nil {
			t.Fatalf("re-sent ApplyRuntime() error = %v", err)
		}
		requireSkew(t, again)
		requireUntouched(t, backend, snapshot)
	})

	t.Run("retry that preflighted again but kept the old expectation", func(t *testing.T) {
		backend := newPipelineMemoryBackend()
		store := newBatchStore(t, backend, fixedFenceKeys{testFenceKeys()})
		mutations := seriesMutations(t, 3, applyVersion(), 0)
		request := execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(), Items: mutations}
		apply := func() execution.StateApplyResult {
			t.Helper()
			if _, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: preflightItems(mutations)}); err != nil {
				t.Fatalf("LoadRuntime() error = %v", err)
			}
			result, err := store.ApplyRuntimeFenced(context.Background(), request, testApplyFence())
			if err != nil {
				t.Fatalf("ApplyRuntimeFenced() error = %v", err)
			}
			return result
		}
		requireAllStatus(t, apply(), execution.StateApplied)
		snapshot := snapshotOf(backend)
		requireSkew(t, apply())
		requireUntouched(t, backend, snapshot)
	})
}

// The same key twice in one request with the same statement is the producer's
// duplicate, not a re-sent write: the later copy reads ALREADY_APPLIED with
// kind repeated_key, so a steady skew count that is really this cannot pose as
// a re-sending client.
func TestApplyRuntimeRepeatedKeyWithTheSameStatementIsNamedRepeatedKey(t *testing.T) {
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, nil)
	first := seriesMutation(t, seriesIdentity(0), applyVersion(), 0, "")
	again := first
	sibling := seriesMutation(t, seriesIdentity(1), applyVersion(), 0, "")
	result, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(),
		Items: []execution.StateMutation{first, sibling, again}})
	if err != nil {
		t.Fatalf("ApplyRuntime() error = %v", err)
	}
	if result.Items[0].Status != execution.StateApplied || result.Items[1].Status != execution.StateApplied {
		t.Fatalf("first copies = %+v / %+v, want applied", result.Items[0], result.Items[1])
	}
	later := result.Items[2]
	if later.Status != execution.StateApplyAlreadyApplied || later.AlreadyApplied != execution.StateAlreadyAppliedRepeatedKey || later.StoredBlobRevision != 1 {
		t.Fatalf("later copy = %+v, want ALREADY_APPLIED kind repeated_key at stored revision 1: the request itself wrote this key, no client re-sent it", later)
	}
	key, _ := RuntimeStateKeyV3("alarmd", first.Identity)
	view := decodeRuntime(backend.values[key], first.Identity, frozenRef(), first.ApplyVersion)
	if view.BlobRevision != 1 {
		t.Fatalf("the later copy rewrote the key: revision %d, want 1", view.BlobRevision)
	}
}

// The same key twice in one request with two different statements is a
// conflict the producer caused, and the item says so: from the status alone it
// reads like a race with another writer, and the fix for those lives in
// different places.
func TestApplyRuntimeRepeatedKeyWithADifferentStatementIsAConflictThatNamesTheRepeat(t *testing.T) {
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, nil)
	first := seriesMutation(t, seriesIdentity(0), applyVersion(), 0, "")
	other := seriesMutation(t, seriesIdentity(0), applyVersion(), 0, "other")
	if first.MutationDigest == other.MutationDigest {
		t.Fatal("fixture: the two statements must differ")
	}
	result, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(),
		Items: []execution.StateMutation{first, other}})
	if err != nil {
		t.Fatalf("ApplyRuntime() error = %v", err)
	}
	if result.Items[0].Status != execution.StateApplied || result.Items[0].RepeatedKey {
		t.Fatalf("first copy = %+v, want applied and not marked repeated", result.Items[0])
	}
	if result.Items[1].Status != execution.StateApplyVersionConflict || !result.Items[1].RepeatedKey {
		t.Fatalf("later different copy = %+v, want STATE_VERSION_CONFLICT marked as a repeated key", result.Items[1])
	}
	// The repeat mark does not replace the comparison: the later copy
	// expected nothing and met the earlier copy at revision 1.
	if later := result.Items[1]; later.VersionConflict != execution.StateVersionConflictRevisionMoved || later.StoredBlobRevision != 1 {
		t.Fatalf("later different copy = %+v, want kind revision_moved at stored revision 1 beside the repeat mark", later)
	}
}

// The sequential path and the witnessed path name their conflicts the same
// way the pipelined one does: the missing key from the site that saw it
// missing, the rest from the one classifier. A retry carrying an old
// expectation into a key that expired is the shape the missing kind exists
// to name, and it is decided without a round trip when the witness already
// says so.
func TestApplyRuntimeSequentialAndWitnessedConflictsNameTheComparison(t *testing.T) {
	version := applyVersion()
	t.Run("sequential", func(t *testing.T) {
		backend := newPipelineMemoryBackend()
		store := newBatchStore(t, backend, nil)
		expired := seriesMutation(t, seriesIdentity(0), version, 2, "")
		reset := seriesMutation(t, seriesIdentity(1), version, 3, "")
		moved := seriesMutation(t, seriesIdentity(2), version, 1, "")
		otherStatement := seriesMutation(t, seriesIdentity(3), version, 1, "")
		keyReset, _ := RuntimeStateKeyV3("alarmd", reset.Identity)
		backend.values[keyReset], _ = encodeRuntimePacked(seriesMutation(t, reset.Identity, version, 0, "fresh"), 1)
		keyMoved, _ := RuntimeStateKeyV3("alarmd", moved.Identity)
		backend.values[keyMoved], _ = encodeRuntimePacked(seriesMutation(t, moved.Identity, version, 1, "theirs"), 2)
		keyOther, _ := RuntimeStateKeyV3("alarmd", otherStatement.Identity)
		backend.values[keyOther], _ = encodeRuntimePacked(seriesMutation(t, otherStatement.Identity, version, 0, "theirs"), 1)
		result, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(),
			Items: []execution.StateMutation{expired, reset, moved, otherStatement}})
		if err != nil {
			t.Fatalf("ApplyRuntime() error = %v", err)
		}
		requireAllStatus(t, result, execution.StateApplyVersionConflict)
		want := []execution.StateApplyItemResult{
			{VersionConflict: execution.StateVersionConflictMissing},
			{VersionConflict: execution.StateVersionConflictRevisionReset, StoredBlobRevision: 1, StoredVersionComparison: execution.ApplyVersionEqual},
			{VersionConflict: execution.StateVersionConflictRevisionMoved, StoredBlobRevision: 2, StoredVersionComparison: execution.ApplyVersionEqual},
			{VersionConflict: execution.StateVersionConflictSameVersionOtherStatement, StoredBlobRevision: 1, StoredVersionComparison: execution.ApplyVersionEqual},
		}
		for index, item := range want {
			got := result.Items[index]
			if got.VersionConflict != item.VersionConflict || got.StoredBlobRevision != item.StoredBlobRevision || got.StoredVersionComparison != item.StoredVersionComparison {
				t.Fatalf("item %d = %+v, want kind %s stored revision %d comparison %q", index, got, item.VersionConflict, item.StoredBlobRevision, item.StoredVersionComparison)
			}
		}
		if backend.casCalls != 0 {
			t.Fatalf("a refused item reached CompareAndSet: cas=%d", backend.casCalls)
		}
	})
	t.Run("witnessed missing with an old expectation", func(t *testing.T) {
		backend := newPipelineMemoryBackend()
		store := newBatchStore(t, backend, fixedFenceKeys{testFenceKeys()})
		fresh := seriesMutation(t, seriesIdentity(0), version, 0, "")
		if _, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: preflightItems([]execution.StateMutation{fresh})}); err != nil {
			t.Fatalf("LoadRuntime() error = %v", err)
		}
		stale := seriesMutation(t, seriesIdentity(0), version, 4, "")
		result, err := store.ApplyRuntimeFenced(context.Background(), execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(),
			Items: []execution.StateMutation{stale}}, testApplyFence())
		if err != nil {
			t.Fatalf("ApplyRuntimeFenced() error = %v", err)
		}
		got := result.Items[0]
		if got.Status != execution.StateApplyVersionConflict || got.VersionConflict != execution.StateVersionConflictMissing || got.StoredBlobRevision != 0 {
			t.Fatalf("item = %+v, want STATE_VERSION_CONFLICT kind missing at stored revision 0", got)
		}
		if backend.pipelines != 0 || backend.casCalls != 0 || len(backend.values) != 0 {
			t.Fatalf("witnessed missing decided with a round trip or a write: pipelines=%d cas=%d keys=%d", backend.pipelines, backend.casCalls, len(backend.values))
		}
	})
}
