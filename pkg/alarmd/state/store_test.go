// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestStoreLoadsMissingValidCorruptAndUnsupportedIndependently(t *testing.T) {
	codec := mustCodec(t)
	backend := newFakeBackend()
	router := &fakeRouter{target: StorageTarget{Name: "monitor-01", Backend: backend}}
	observations := make([]Observation, 0, 2)
	store := mustStoreWithObserver(t, codec, router, ObserverFunc(func(_ context.Context, observation Observation) {
		observations = append(observations, observation)
	}))
	requirement := requirement(5, "5", 2, 4)
	identities := []RuntimeIdentity{
		storeIdentity("11", "a"), storeIdentity("12", "b"), storeIdentity("13", "c"), storeIdentity("14", "d"),
	}
	valid, err := NewWindow([]LevelRequirement{requirement})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	mustApply(t, valid, []StatePoint{point(100, "a", fact(requirement, LevelFactAnomalous))})
	validBlob, err := codec.Encode(valid)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	validKey, _ := identities[1].Key("alarmd")
	corruptKey, _ := identities[2].Key("alarmd")
	unsupportedKey, _ := identities[3].Key("alarmd")
	unsupported := append([]byte(nil), validBlob...)
	unsupported[4]++
	backend.values[validKey] = validBlob
	backend.values[corruptKey] = []byte("broken")
	backend.values[unsupportedKey] = unsupported

	result, err := store.LoadWindows(context.Background(), LoadWindowsRequest{Items: loadSpecs(identities, requirement)})
	if err != nil {
		t.Fatalf("LoadWindows() error = %v", err)
	}
	if got, want := len(result.Items), len(identities); got != want {
		t.Fatalf("items = %d, want %d", got, want)
	}
	wantStatuses := []LoadStatus{LoadMissing, LoadFound, LoadResetCorrupt, LoadUnavailable}
	for index, want := range wantStatuses {
		if result.Items[index].Status != want {
			t.Fatalf("item[%d] status = %q, want %q", index, result.Items[index].Status, want)
		}
	}
	if result.Items[0].Window == nil || result.Items[2].Window == nil {
		t.Fatal("missing and corrupt state must yield an empty WARMING window")
	}
	if result.Items[3].Window != nil || !errors.Is(result.Items[3].Err, ErrUnsupportedState) {
		t.Fatalf("unsupported item = %+v, want isolated unavailable", result.Items[3])
	}
	history, ok := result.Items[1].Window.History(5)
	if !ok || history.Summarize(100, 1).AnomalyCount != 1 {
		t.Fatal("valid state history was not loaded")
	}
	if len(router.strategies) != len(identities) || router.strategies[0] != "11" {
		t.Fatalf("router strategies = %v, want strategy_id routing", router.strategies)
	}
	if len(backend.mgetBatches) != 2 {
		t.Fatalf("MGET batches = %d, want max-2-key bounded batches", len(backend.mgetBatches))
	}
	if len(observations) != 3 {
		t.Fatalf("observations = %+v, want load/decode plus explicit history summary", observations)
	}
	if observations[0].Operation != OperationLoad || observations[0].BackendCalls != 2 {
		t.Fatalf("load observation = %+v", observations[0])
	}
	loadObservation := observations[1]
	if loadObservation.Stage != StageDependencyLoaded || loadObservation.Result != OperationPartial ||
		loadObservation.Operation != OperationDecode || loadObservation.Codec != CodecNoneV1 || loadObservation.BackendCalls != 0 ||
		loadObservation.TouchedKeys != 4 || loadObservation.FoundKeys != 1 || loadObservation.MissingKeys != 1 ||
		loadObservation.ResetCorruptKeys != 1 || loadObservation.UnsupportedKeys != 1 || loadObservation.UnavailableKeys != 0 ||
		loadObservation.DecodeBytes != len(validBlob)+len(backend.values[corruptKey])+len(unsupported) {
		t.Fatalf("load observation = %+v", loadObservation)
	}
	if _, err := store.WriteWindows(context.Background(), WriteWindowsRequest{Items: []LoadedWindow{result.Items[2]}}); err != nil {
		t.Fatalf("WriteWindows(unchanged corrupt reset) error = %v", err)
	}
	if len(backend.setBatches) != 0 {
		t.Fatal("corrupt state was overwritten before a changed next state existed")
	}
	lastObservation := observations[len(observations)-1]
	if lastObservation.Stage != StageStateCommitted || lastObservation.NoopKeys != 1 ||
		lastObservation.Result != OperationSucceeded {
		t.Fatalf("noop observation = %+v", lastObservation)
	}
}

func TestStoreWritesOnlyChangedWindowsWithTTLAndRetriesFailedBatch(t *testing.T) {
	codec := mustCodec(t)
	backend := newFakeBackend()
	store := mustStore(t, codec, &fakeRouter{target: StorageTarget{Name: "monitor-01", Backend: backend}})
	requirement := requirement(5, "5", 2, 4)
	request := LoadWindowsRequest{Items: loadSpecs([]RuntimeIdentity{storeIdentity("11", "a"), storeIdentity("12", "b")}, requirement)}
	loaded, err := store.LoadWindows(context.Background(), request)
	if err != nil {
		t.Fatalf("LoadWindows() error = %v", err)
	}
	mustApply(t, loaded.Items[0].Window, []StatePoint{point(100, "a", fact(requirement, LevelFactNormal))})

	written, err := store.WriteWindows(context.Background(), WriteWindowsRequest{Items: loaded.Items})
	if err != nil {
		t.Fatalf("WriteWindows() error = %v", err)
	}
	if written.Items[0].Status != WritePersisted || written.Items[1].Status != WriteNoop {
		t.Fatalf("write statuses = %+v", written.Items)
	}
	if len(backend.setBatches) != 1 || len(backend.setBatches[0]) != 1 {
		t.Fatalf("SET batches = %+v, want one changed key", backend.setBatches)
	}
	if backend.setBatches[0][0].TTL != 5*time.Minute {
		t.Fatalf("TTL = %v, want retention 4m + restart margin 1m", backend.setBatches[0][0].TTL)
	}
	if loaded.Items[0].Window.Changed() {
		t.Fatal("successful write did not mark window persisted")
	}

	mustApply(t, loaded.Items[0].Window, []StatePoint{point(160, "b", fact(requirement, LevelFactAnomalous))})
	backend.writeErr = errors.New("redis unavailable")
	if _, err := store.WriteWindows(context.Background(), WriteWindowsRequest{Items: loaded.Items}); err == nil {
		t.Fatal("WriteWindows() error = nil, want retryable backend error")
	}
	if !loaded.Items[0].Window.Changed() {
		t.Fatal("failed write cleared changed state")
	}
	backend.writeErr = nil
	if _, err := store.WriteWindows(context.Background(), WriteWindowsRequest{Items: loaded.Items}); err != nil {
		t.Fatalf("WriteWindows(retry) error = %v", err)
	}
	if loaded.Items[0].Window.Changed() {
		t.Fatal("retry did not persist changed state")
	}
}

func TestStoreDoesNotOverwriteUnsupportedState(t *testing.T) {
	codec := mustCodec(t)
	backend := newFakeBackend()
	store := mustStore(t, codec, &fakeRouter{target: StorageTarget{Name: "monitor-01", Backend: backend}})
	requirement := requirement(1, "1", 1, 2)
	identity := storeIdentity("11", "a")
	window, _ := NewWindow([]LevelRequirement{requirement})
	blob, _ := codec.Encode(window)
	blob[4]++
	key, _ := identity.Key("alarmd")
	backend.values[key] = blob

	loaded, err := store.LoadWindows(context.Background(), LoadWindowsRequest{Items: loadSpecs([]RuntimeIdentity{identity}, requirement)})
	if err != nil {
		t.Fatalf("LoadWindows() error = %v", err)
	}
	written, err := store.WriteWindows(context.Background(), WriteWindowsRequest{Items: loaded.Items})
	if err != nil {
		t.Fatalf("WriteWindows() error = %v", err)
	}
	if written.Items[0].Status != WriteUnavailable {
		t.Fatalf("write status = %q, want caller-visible UNAVAILABLE", written.Items[0].Status)
	}
	if len(backend.setBatches) != 0 {
		t.Fatal("unsupported state was overwritten")
	}
}

func TestStoreExposesPostAdmissionBudgetAsInvariantViolation(t *testing.T) {
	codec, err := NewCodec(CodecLimits{MaxLevels: 2, MaxPoints: 1, MaxEncodedBytes: 256})
	if err != nil {
		t.Fatalf("NewCodec() error = %v", err)
	}
	backend := newFakeBackend()
	observations := make([]Observation, 0, 1)
	store := mustStoreWithObserver(t, codec, &fakeRouter{target: StorageTarget{Name: "monitor-01", Backend: backend}},
		ObserverFunc(func(_ context.Context, observation Observation) {
			observations = append(observations, observation)
		}))
	requirement := requirement(1, "1", 1, 2)
	window, _ := NewWindow([]LevelRequirement{requirement})
	mustApply(t, window, []StatePoint{
		point(100, "a", fact(requirement, LevelFactNormal)),
		point(160, "b", fact(requirement, LevelFactAnomalous)),
	})
	identity := storeIdentity("11", "a")
	key, _ := identity.Key("alarmd")
	written, err := store.WriteWindows(context.Background(), WriteWindowsRequest{Items: []LoadedWindow{{
		Identity: identity, Key: key, Requirements: []LevelRequirement{requirement}, Window: window,
	}}})
	if err != nil {
		t.Fatalf("WriteWindows() shared error = %v, want item invariant violation", err)
	}
	if written.Items[0].Status != WriteInvariantViolation || !errors.Is(written.Items[0].Err, ErrStateBudget) {
		t.Fatalf("write item = %+v, want incomplete budget invariant violation", written.Items[0])
	}
	if len(backend.setBatches) != 0 || !window.Changed() {
		t.Fatal("terminal state was written or marked persisted")
	}
	if len(observations) != 1 || observations[0].Stage != StageStateCommitted || observations[0].Operation != OperationEncode ||
		observations[0].Result != OperationFailed || observations[0].InvariantKeys != 1 ||
		observations[0].BudgetViolations != 1 || observations[0].ReasonCode != "" {
		t.Fatalf("observations = %+v, want budget invariant commit result", observations)
	}
}

func TestStoreRejectsMutatedPhysicalKey(t *testing.T) {
	codec := mustCodec(t)
	backend := newFakeBackend()
	store := mustStore(t, codec, &fakeRouter{target: StorageTarget{Name: "monitor-01", Backend: backend}})
	requirement := requirement(1, "1", 1, 2)
	loaded, err := store.LoadWindows(context.Background(), LoadWindowsRequest{
		Items: loadSpecs([]RuntimeIdentity{storeIdentity("11", "a")}, requirement),
	})
	if err != nil {
		t.Fatalf("LoadWindows() error = %v", err)
	}
	mustApply(t, loaded.Items[0].Window, []StatePoint{point(100, "a", fact(requirement, LevelFactNormal))})
	loaded.Items[0].Key = "alarmd:runtime:v1:w:wrong"
	if _, err := store.WriteWindows(context.Background(), WriteWindowsRequest{Items: loaded.Items}); err == nil {
		t.Fatal("WriteWindows() error = nil, want physical key mismatch")
	}
	if len(backend.setBatches) != 0 {
		t.Fatal("mutated physical key was written")
	}
}

func TestStoreRejectsDuplicateRuntimeKey(t *testing.T) {
	codec := mustCodec(t)
	backend := newFakeBackend()
	store := mustStore(t, codec, &fakeRouter{target: StorageTarget{Name: "monitor-01", Backend: backend}})
	requirement := requirement(1, "1", 1, 2)
	identity := storeIdentity("11", "a")
	if _, err := store.LoadWindows(context.Background(), LoadWindowsRequest{
		Items: loadSpecs([]RuntimeIdentity{identity, identity}, requirement),
	}); err == nil {
		t.Fatal("LoadWindows() error = nil, want duplicate RuntimeKey rejection")
	}
	if len(backend.mgetBatches) != 0 {
		t.Fatal("duplicate RuntimeKey reached backend")
	}
}

func TestStoreIsolatesInvalidRuntimeIdentity(t *testing.T) {
	codec := mustCodec(t)
	backend := newFakeBackend()
	store := mustStore(t, codec, &fakeRouter{target: StorageTarget{Name: "monitor-01", Backend: backend}})
	requirement := requirement(1, "1", 1, 2)
	invalid := storeIdentity("11", "a")
	invalid.StateCompatibilityHash = "invalid"
	loaded, err := store.LoadWindows(context.Background(), LoadWindowsRequest{
		Items: loadSpecs([]RuntimeIdentity{invalid, storeIdentity("12", "b")}, requirement),
	})
	if err != nil {
		t.Fatalf("LoadWindows() error = %v, deterministic item must be isolated", err)
	}
	if loaded.Items[0].Status != LoadUnavailable || loaded.Items[0].Window != nil || loaded.Items[0].Err == nil {
		t.Fatalf("invalid item = %+v, want isolated unavailable", loaded.Items[0])
	}
	if loaded.Items[1].Status != LoadMissing || loaded.Items[1].Window == nil {
		t.Fatalf("valid sibling = %+v, want missing WARMING window", loaded.Items[1])
	}
}

func TestStoreBoundsMGetByWorstCaseResponseBeforeRequest(t *testing.T) {
	codec, err := NewCodec(CodecLimits{MaxLevels: 2, MaxPoints: 4, MaxEncodedBytes: 256})
	if err != nil {
		t.Fatalf("NewCodec() error = %v", err)
	}
	backend := newFakeBackend()
	store, err := NewStore(StoreOptions{
		Prefix: "alarmd", Codec: codec,
		Router: &fakeRouter{target: StorageTarget{Name: "monitor-01", Backend: backend}},
		Limits: StoreLimits{
			MaxKeysPerBatch: 10, MaxKeyBytesPerBatch: 4096, MaxLoadedBytes: 512, MaxWrittenBytes: 4096,
		},
		MinTTL: time.Minute, MaxTTL: time.Hour, RestartMargin: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	requirement := requirement(1, "1", 1, 2)
	_, err = store.LoadWindows(context.Background(), LoadWindowsRequest{Items: loadSpecs([]RuntimeIdentity{
		storeIdentity("11", "a"), storeIdentity("12", "b"), storeIdentity("13", "c"),
	}, requirement)})
	if err != nil {
		t.Fatalf("LoadWindows() error = %v", err)
	}
	if len(backend.mgetBatches) != 2 || len(backend.mgetBatches[0]) != 2 || len(backend.mgetBatches[1]) != 1 {
		t.Fatalf("MGET batches = %v, want [2,1] from 2*256 response admission", backend.mgetBatches)
	}

	_, err = NewStore(StoreOptions{
		Prefix: "alarmd", Codec: codec,
		Router: &fakeRouter{target: StorageTarget{Name: "monitor-01", Backend: backend}},
		Limits: StoreLimits{
			MaxKeysPerBatch: 1, MaxKeyBytesPerBatch: 1024, MaxLoadedBytes: 255, MaxWrittenBytes: 1024,
		},
		MinTTL: time.Minute, MaxTTL: time.Hour,
	})
	if !errors.Is(err, ErrStateBudget) {
		t.Fatalf("NewStore(load budget) error = %v, want state budget", err)
	}
}

func TestStateTTLRejectsUnsatisfiedHardMaximum(t *testing.T) {
	requirement := requirement(1, "1", 2, 4)
	if _, err := StateTTL([]LevelRequirement{requirement}, time.Minute, time.Minute, 4*time.Minute); !errors.Is(err, ErrStateBudget) {
		t.Fatalf("StateTTL() error = %v, want budget error", err)
	}
}

func TestStoreReportsBoundedBackendOperationsToObserver(t *testing.T) {
	codec := mustCodec(t)
	backend := newFakeBackend()
	observations := make([]Observation, 0, 2)
	store, err := NewStore(StoreOptions{
		Prefix: "alarmd", Codec: codec,
		Router: &fakeRouter{target: StorageTarget{Name: "monitor-01", Backend: backend}},
		Limits: StoreLimits{MaxKeysPerBatch: 2, MaxKeyBytesPerBatch: 1024, MaxLoadedBytes: 16 << 10, MaxWrittenBytes: 16 << 10},
		MinTTL: time.Minute, MaxTTL: time.Hour, RestartMargin: time.Minute,
		Observer: ObserverFunc(func(_ context.Context, observation Observation) {
			observations = append(observations, observation)
		}),
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	requirement := requirement(1, "1", 1, 2)
	loaded, err := store.LoadWindows(context.Background(), LoadWindowsRequest{
		Items: loadSpecs([]RuntimeIdentity{storeIdentity("11", "a")}, requirement),
	})
	if err != nil {
		t.Fatalf("LoadWindows() error = %v", err)
	}
	mustApply(t, loaded.Items[0].Window, []StatePoint{point(100, "a", fact(requirement, LevelFactNormal))})
	if _, err := store.WriteWindows(context.Background(), WriteWindowsRequest{Items: loaded.Items}); err != nil {
		t.Fatalf("WriteWindows() error = %v", err)
	}
	history, _ := loaded.Items[0].Window.History(1)
	_ = history.SummarizeContext(context.Background(), 100, 1)
	if len(observations) != 6 || observations[0].Operation != OperationLoad || observations[1].Operation != OperationDecode ||
		observations[2].Operation != OperationTransition || observations[3].Operation != OperationEncode ||
		observations[4].Operation != OperationWrite || observations[5].Operation != OperationSample {
		t.Fatalf("observations = %+v, want load/decode/transition/encode/write/sample", observations)
	}
	if observations[0].Result != OperationSucceeded || observations[0].Target != "monitor-01" ||
		observations[0].TouchedKeys != 1 || observations[0].BackendCalls != 1 ||
		observations[0].RequestBytes <= 0 || observations[0].Duration < 0 || observations[0].Codec != CodecNoneV1 {
		t.Fatalf("load observation = %+v", observations[0])
	}
	if observations[1].MissingKeys != 1 || observations[1].DecodeBytes != 0 {
		t.Fatalf("decode observation = %+v", observations[1])
	}
	if observations[2].AppliedPoints != 1 {
		t.Fatalf("apply observation = %+v", observations[2])
	}
	if observations[3].EncodeBytes <= 0 || observations[3].BackendCalls != 0 ||
		observations[4].PersistedKeys != 1 || observations[4].BackendCalls != 1 || observations[4].Target != "monitor-01" {
		t.Fatalf("encode/write observations = %+v / %+v", observations[3], observations[4])
	}
	if observations[5].FullSummaries != 1 {
		t.Fatalf("summary observation = %+v", observations[len(observations)-1])
	}

	backend.readErr = errors.New("redis unavailable")
	if _, err := store.LoadWindows(context.Background(), LoadWindowsRequest{
		Items: loadSpecs([]RuntimeIdentity{storeIdentity("12", "b")}, requirement),
	}); err == nil {
		t.Fatal("LoadWindows() error = nil, want dependency error")
	}
	last := observations[len(observations)-1]
	if last.Operation != OperationLoad || last.Result != OperationFailed ||
		last.ReasonCode != contract.ReasonRedisUnavailable || last.UnavailableKeys != 1 {
		t.Fatalf("failed observation = %+v", last)
	}
}

func mustCodec(t *testing.T) *Codec {
	t.Helper()
	codec, err := NewCodec(CodecLimits{MaxLevels: 8, MaxPoints: 32, MaxEncodedBytes: 4096})
	if err != nil {
		t.Fatalf("NewCodec() error = %v", err)
	}
	return codec
}

func mustStore(t *testing.T, codec *Codec, router StorageRouter) *Store {
	return mustStoreWithObserver(t, codec, router, nil)
}

func mustStoreWithObserver(t *testing.T, codec *Codec, router StorageRouter, observer Observer) *Store {
	t.Helper()
	store, err := NewStore(StoreOptions{
		Prefix: "alarmd", Codec: codec, Router: router,
		Limits: StoreLimits{MaxKeysPerBatch: 2, MaxKeyBytesPerBatch: 1024, MaxLoadedBytes: 16 << 10, MaxWrittenBytes: 16 << 10},
		MinTTL: time.Minute, MaxTTL: time.Hour, RestartMargin: time.Minute,
		Observer: observer,
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}

func storeIdentity(strategyID, dimensionDigit string) RuntimeIdentity {
	return RuntimeIdentity{
		TenantID: "system", BusinessID: "-1", StrategyID: strategyID,
		StateCompatibilityHash: strings.Repeat("e", 64), DimensionIdentityDigest: strings.Repeat(dimensionDigit, 64),
	}
}

func loadSpecs(identities []RuntimeIdentity, requirement LevelRequirement) []LoadWindowSpec {
	items := make([]LoadWindowSpec, len(identities))
	for index, identity := range identities {
		items[index] = LoadWindowSpec{Identity: identity, Requirements: []LevelRequirement{requirement}}
	}
	return items
}

type fakeRouter struct {
	target     StorageTarget
	strategies []string
	err        error
}

func (router *fakeRouter) Route(_ string, strategyID string) (StorageTarget, error) {
	router.strategies = append(router.strategies, strategyID)
	return router.target, router.err
}

type fakeBackend struct {
	values      map[string][]byte
	mgetBatches [][]string
	setBatches  [][]BackendWrite
	readErr     error
	writeErr    error
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{values: make(map[string][]byte)}
}

func (backend *fakeBackend) MGet(_ context.Context, keys []string) ([][]byte, error) {
	backend.mgetBatches = append(backend.mgetBatches, append([]string(nil), keys...))
	if backend.readErr != nil {
		return nil, backend.readErr
	}
	values := make([][]byte, len(keys))
	for index, key := range keys {
		if value, exists := backend.values[key]; exists {
			values[index] = append([]byte(nil), value...)
		}
	}
	return values, nil
}

func (backend *fakeBackend) SetMany(_ context.Context, writes []BackendWrite) error {
	batch := append([]BackendWrite(nil), writes...)
	backend.setBatches = append(backend.setBatches, batch)
	if backend.writeErr != nil {
		return backend.writeErr
	}
	for _, write := range writes {
		backend.values[write.Key] = append([]byte(nil), write.Value...)
	}
	return nil
}
