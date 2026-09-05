// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

type casMemoryBackend struct {
	values   map[string][]byte
	conflict bool
}

func (backend *casMemoryBackend) MGet(_ context.Context, keys []string) ([][]byte, error) {
	result := make([][]byte, len(keys))
	for i, key := range keys {
		result[i] = append([]byte(nil), backend.values[key]...)
	}
	return result, nil
}
func (*casMemoryBackend) SetMany(context.Context, []BackendWrite) error { return nil }
func (backend *casMemoryBackend) CompareAndSet(_ context.Context, key string, expected []byte, missing bool, value []byte, _ time.Duration) (bool, error) {
	if backend.conflict {
		return false, nil
	}
	current, found := backend.values[key]
	if found == missing || (!missing && string(current) != string(expected)) {
		return false, nil
	}
	backend.values[key] = append([]byte(nil), value...)
	return true, nil
}

func TestExecutionStateIdentityIsBoundedAndLevelIndependent(t *testing.T) {
	identity := execution.StateKeyIdentity{
		Plan:            execution.PlanIdentity{TenantID: strings.Repeat("tenant", 100), BusinessID: "2", StrategyID: "9"},
		StateGeneration: "generation-1", SeriesIdentityDigest: execution.SeriesIdentityDigest(strings.Repeat("a", 64)),
	}
	key, err := RuntimeStateKeyV2("alarmd", identity)
	if err != nil {
		t.Fatalf("RuntimeStateKeyV2() error = %v", err)
	}
	if len(key) > 256 || !strings.Contains(key, ":runtime:v2:") {
		t.Fatalf("RuntimeStateKeyV2() = %q", key)
	}
	if key2, _ := RuntimeStateKeyV2("alarmd", identity); key2 != key {
		t.Fatalf("key is not deterministic: %q != %q", key, key2)
	}
}

func TestExecutionStateIdentityRejectsAmbiguousNumericFields(t *testing.T) {
	for _, plan := range []execution.PlanIdentity{
		{TenantID: "tenant", BusinessID: "1:2", StrategyID: "3"},
		{TenantID: "tenant", BusinessID: "01", StrategyID: "3"},
		{TenantID: "tenant", BusinessID: "1", StrategyID: "2:3"},
	} {
		identity := stateIdentityV2()
		identity.Plan = plan
		if _, err := RuntimeStateKeyV2("alarmd", identity); err == nil {
			t.Fatalf("RuntimeStateKeyV2(%+v) accepted ambiguous identity", plan)
		}
	}
	left, right := stateIdentityV2(), stateIdentityV2()
	left.Plan.BusinessID, left.Plan.StrategyID = "1", "23"
	right.Plan.BusinessID, right.Plan.StrategyID = "12", "3"
	leftKey, _ := RuntimeStateKeyV2("alarmd", left)
	rightKey, _ := RuntimeStateKeyV2("alarmd", right)
	if leftKey == rightKey {
		t.Fatalf("numeric tuple collision: %q", leftKey)
	}
	if _, err := RuntimeStateKeyV2(strings.Repeat("p", 65), left); err == nil {
		t.Fatal("oversized prefix was accepted")
	}
}

func TestPlanGapIdentityHasNoSeriesOrLevel(t *testing.T) {
	identity := execution.PlanGapIdentity{
		Plan:            execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "9"},
		StateGeneration: "generation-1",
	}
	key, err := PlanGapKeyV2("alarmd", identity)
	if err != nil {
		t.Fatalf("PlanGapKeyV2() error = %v", err)
	}
	if !strings.Contains(key, ":gap:v2:") || len(key) > 256 {
		t.Fatalf("PlanGapKeyV2() = %q", key)
	}
}

func TestApplyGapRejectsInvalidIdentityWithoutCallingStorage(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	router, _ := NewFixedRouter("monitor-01", backend)
	store, _ := NewExecutionStore(ExecutionStoreOptions{Prefix: "alarmd", Router: router, MaxValueBytes: 4096, MaxItemsPerCall: 4, RuntimeTTL: time.Hour})
	identity := execution.PlanGapIdentity{Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "9:1"}, StateGeneration: "generation"}
	mutation, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{Identity: identity, ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1", Scopes: []execution.GapScopeMutation{{Kind: execution.GapOpen, ReasonCode: execution.ReasonCode(contract.ReasonHistoryGapped), RequiredFullSlots: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.ApplyGap(context.Background(), execution.GapGuardApplyRequest{Contract: frozenRef(), Items: []execution.PlanGapMutation{mutation}})
	if err != nil || result.Items[0].Status != execution.GapGuardRejected {
		t.Fatalf("ApplyGap(invalid identity) = (%+v, %v)", result, err)
	}
	if len(backend.values) != 0 {
		t.Fatal("invalid identity reached storage")
	}
}

func TestExecutionStoreDoesNotResetCorruptRuntimeState(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	router, _ := NewFixedRouter("monitor-01", backend)
	store, err := NewExecutionStore(ExecutionStoreOptions{Prefix: "alarmd", Router: router, MaxValueBytes: 4096, MaxItemsPerCall: 4, RuntimeTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	identity := stateIdentityV2()
	key, _ := RuntimeStateKeyV2("alarmd", identity)
	backend.values[key] = []byte("not-json")
	request := execution.StatePreflightRequest{Contract: frozenRef(), Items: []execution.StatePreflightItem{{Identity: identity, ApplyVersion: applyVersion()}}}
	loaded, err := store.LoadRuntime(context.Background(), request)
	if err != nil || loaded.Items[0].Status != execution.StateDeterministicInvalid {
		t.Fatalf("LoadRuntime() = (%+v, %v)", loaded, err)
	}
	if string(backend.values[key]) != "not-json" {
		t.Fatal("corrupt value was overwritten")
	}
	mutation, buildErr := execution.BuildStateMutation(execution.StateMutation{Identity: identity, ApplyVersion: applyVersion(), AffectedRecords: []execution.RecordAnchor{{RecordID: "r1", SourceTime: 60}}, Levels: []execution.RuntimeLevelStateMutation{{LevelID: 1, LevelStateCompatibility: "compat", HistoryCompleteness: execution.HistoryFull, WarmupRequirementRef: "warm", LastProcessedEventTime: 60}}, Points: []execution.StateHistoryPoint{{RecordID: "r1", SourceTime: 60, Levels: []execution.StateLevelFact{{LevelID: 1, DetectFingerprint: "detect", Result: execution.LevelFactNormal}}}}})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	applied, applyErr := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{Contract: frozenRef(), Items: []execution.StateMutation{mutation}})
	if applyErr != nil || applied.Items[0].Status != execution.StateApplyDeterministicInvalid {
		t.Fatalf("ApplyRuntime(corrupt) = (%+v, %v)", applied, applyErr)
	}
	if string(backend.values[key]) != "not-json" {
		t.Fatal("ApplyRuntime overwrote corrupt value")
	}
}

func TestRuntimeSeriesGuardDeterminesPersistedStatus(t *testing.T) {
	mutation, err := execution.BuildStateMutation(execution.StateMutation{Identity: stateIdentityV2(), ApplyVersion: applyVersion(), AffectedRecords: []execution.RecordAnchor{{RecordID: "r1", SourceTime: 60}}, SeriesGuard: &execution.StateGuardFact{Status: execution.HistoryWarming, ReasonCode: execution.ReasonCode(contract.ReasonHistoryWarming), WarmupRequirementRef: "series-warm"}, Levels: []execution.RuntimeLevelStateMutation{{LevelID: 1, LevelStateCompatibility: "compat", HistoryCompleteness: execution.HistoryFull, WarmupRequirementRef: "warm", LastProcessedEventTime: 60}}, Points: []execution.StateHistoryPoint{{RecordID: "r1", SourceTime: 60, Levels: []execution.StateLevelFact{{LevelID: 1, DetectFingerprint: "detect", Result: execution.LevelFactNormal}}}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := encodeRuntime(mutation, 1)
	if err != nil {
		t.Fatal(err)
	}
	view := decodeRuntime(raw, mutation.Identity, frozenRef(), mutation.ApplyVersion)
	if view.Status != execution.StateFoundWarming {
		t.Fatalf("status = %s, want FOUND_WARMING", view.Status)
	}
}

func TestGapWarmupCountsFullSlotsMonotonicallyAndResetsOnScheduleChange(t *testing.T) {
	scope := execution.GapScope{LevelID: 1, HasLevel: true}
	reason := execution.ReasonCode(contract.ReasonHistoryGapped)
	previous := []execution.GapScopeState{{Scope: scope, Status: execution.GapStatusGapped, ReasonCode: reason, RequiredFullSlots: 2}}
	mutation := []execution.GapScopeMutation{{Scope: scope, Kind: execution.GapWarmup, ReasonCode: reason, RequiredFullSlots: 2}}
	first := applyGapScopes(previous, mutation, "r1", "r1")
	second := applyGapScopes(first, mutation, "r1", "r1")
	third := applyGapScopes(second, mutation, "r1", "r1")
	if first[0].ObservedFullSlots != 1 || second[0].ObservedFullSlots != 2 || third[0].ObservedFullSlots != 2 {
		t.Fatalf("warmup counts = %d,%d,%d", first[0].ObservedFullSlots, second[0].ObservedFullSlots, third[0].ObservedFullSlots)
	}
	reset := applyGapScopes(second, mutation, "r1", "r2")
	if reset[0].ObservedFullSlots != 1 {
		t.Fatalf("schedule change count = %d, want 1", reset[0].ObservedFullSlots)
	}
}

func TestExecutionStoreExactCASAndReplay(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	router, _ := NewFixedRouter("monitor-01", backend)
	store, _ := NewExecutionStore(ExecutionStoreOptions{Prefix: "alarmd", Router: router, MaxValueBytes: 4096, MaxItemsPerCall: 4, RuntimeTTL: time.Hour})
	mutation, err := execution.BuildStateMutation(execution.StateMutation{Identity: stateIdentityV2(), ApplyVersion: applyVersion(),
		AffectedRecords: []execution.RecordAnchor{{RecordID: "r1", SourceTime: 60}},
		Levels:          []execution.RuntimeLevelStateMutation{{LevelID: 1, LevelStateCompatibility: "compat", HistoryCompleteness: execution.HistoryFull, WarmupRequirementRef: "warm", LastProcessedEventTime: 60}},
		Points:          []execution.StateHistoryPoint{{RecordID: "r1", SourceTime: 60, Levels: []execution.StateLevelFact{{LevelID: 1, DetectFingerprint: "detect", Result: execution.LevelFactNormal}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := execution.StateApplyRequest{Contract: frozenRef(), Items: []execution.StateMutation{mutation}}
	first, err := store.ApplyRuntime(context.Background(), request)
	if err != nil || first.Items[0].Status != execution.StateApplied {
		t.Fatalf("first ApplyRuntime() = (%+v, %v)", first, err)
	}
	request.Items[0].ExpectedBlobRevision = 1
	replay, err := store.ApplyRuntime(context.Background(), request)
	if err != nil || replay.Items[0].Status != execution.StateApplyAlreadyApplied {
		t.Fatalf("replay ApplyRuntime() = (%+v, %v)", replay, err)
	}
}

func TestExecutionStoreAdmissionAndCASBudgetStatuses(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte), conflict: true}
	router, _ := NewFixedRouter("monitor-01", backend)
	mutation, err := execution.BuildStateMutation(execution.StateMutation{Identity: stateIdentityV2(), ApplyVersion: applyVersion(), AffectedRecords: []execution.RecordAnchor{{RecordID: "r1", SourceTime: 60}}, Levels: []execution.RuntimeLevelStateMutation{{LevelID: 1, LevelStateCompatibility: "compat", HistoryCompleteness: execution.HistoryFull, WarmupRequirementRef: "warm", LastProcessedEventTime: 60}}, Points: []execution.StateHistoryPoint{{RecordID: "r1", SourceTime: 60, Levels: []execution.StateLevelFact{{LevelID: 1, DetectFingerprint: "detect", Result: execution.LevelFactNormal}}}}})
	if err != nil {
		t.Fatal(err)
	}
	request := execution.StateApplyRequest{Contract: frozenRef(), Items: []execution.StateMutation{mutation}}
	small, _ := NewExecutionStore(ExecutionStoreOptions{Prefix: "alarmd", Router: router, MaxValueBytes: 1, MaxItemsPerCall: 4, RuntimeTTL: time.Hour})
	admission, err := small.AdmitRuntime(context.Background(), request)
	if err != nil || admission.Items[0].Status != execution.StateAdmissionDeterministicInvalid || admission.Items[0].ReasonCode != execution.ReasonCode(contract.ReasonStateBudgetExceeded) {
		t.Fatalf("AdmitRuntime() = (%+v, %v)", admission, err)
	}
	store, _ := NewExecutionStore(ExecutionStoreOptions{Prefix: "alarmd", Router: router, MaxValueBytes: 4096, MaxItemsPerCall: 4, RuntimeTTL: time.Hour})
	applied, err := store.ApplyRuntime(context.Background(), request)
	if err != nil || applied.Items[0].Status != execution.StateApplyCASConflict {
		t.Fatalf("ApplyRuntime(conflict) = (%+v, %v)", applied, err)
	}
}

func TestRuntimeOversizeIsLocalAndApplyDoesNotOverwrite(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	router, _ := NewFixedRouter("monitor-01", backend)
	goodIdentity := stateIdentityV2()
	goodIdentity.SeriesIdentityDigest = "good"
	badIdentity := stateIdentityV2()
	badIdentity.SeriesIdentityDigest = "oversize"
	goodMutation, err := execution.BuildStateMutation(execution.StateMutation{Identity: goodIdentity, ApplyVersion: applyVersion(), AffectedRecords: []execution.RecordAnchor{{RecordID: "r1", SourceTime: 60}}, Levels: []execution.RuntimeLevelStateMutation{{LevelID: 1, LevelStateCompatibility: "compat", HistoryCompleteness: execution.HistoryFull, WarmupRequirementRef: "warm", LastProcessedEventTime: 60}}, Points: []execution.StateHistoryPoint{{RecordID: "r1", SourceTime: 60, Levels: []execution.StateLevelFact{{LevelID: 1, DetectFingerprint: "detect", Result: execution.LevelFactNormal}}}}})
	if err != nil {
		t.Fatal(err)
	}
	goodRaw, _ := encodeRuntime(goodMutation, 1)
	limit := len(goodRaw) + 16
	goodKey, _ := RuntimeStateKeyV2("alarmd", goodIdentity)
	badKey, _ := RuntimeStateKeyV2("alarmd", badIdentity)
	backend.values[goodKey], backend.values[badKey] = goodRaw, []byte(strings.Repeat("x", limit+1))
	store, _ := NewExecutionStore(ExecutionStoreOptions{Prefix: "alarmd", Router: router, MaxValueBytes: limit, MaxItemsPerCall: 4, RuntimeTTL: time.Hour})
	loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: []execution.StatePreflightItem{{Identity: goodIdentity, ApplyVersion: applyVersion()}, {Identity: badIdentity, ApplyVersion: applyVersion()}}})
	if err != nil || loaded.Items[0].Status != execution.StateFoundReady || loaded.Items[1].Status != execution.StateDeterministicInvalid {
		t.Fatalf("LoadRuntime(mixed) = (%+v, %v)", loaded, err)
	}
	badMutation := goodMutation
	badMutation.Identity, badMutation.MutationDigest = badIdentity, ""
	badMutation, err = execution.BuildStateMutation(badMutation)
	if err != nil {
		t.Fatal(err)
	}
	original := append([]byte(nil), backend.values[badKey]...)
	applied, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{Contract: frozenRef(), Items: []execution.StateMutation{badMutation}})
	if err != nil || applied.Items[0].Status != execution.StateApplyDeterministicInvalid || string(backend.values[badKey]) != string(original) {
		t.Fatalf("ApplyRuntime(oversize) = (%+v, %v)", applied, err)
	}
}

func TestExecutionStorePlanGapRoundTripAndTombstone(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	router, _ := NewFixedRouter("monitor-01", backend)
	store, _ := NewExecutionStore(ExecutionStoreOptions{Prefix: "alarmd", Router: router, MaxValueBytes: 4096, MaxItemsPerCall: 4, RuntimeTTL: time.Hour})
	identity := execution.PlanGapIdentity{Plan: stateIdentityV2().Plan, StateGeneration: "generation"}
	opened, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{Identity: identity, ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1",
		Scopes: []execution.GapScopeMutation{{Scope: execution.GapScope{LevelID: 1, HasLevel: true}, Kind: execution.GapOpen,
			ReasonCode: execution.ReasonCode(contract.ReasonHistoryGapped), RequiredFullSlots: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	apply, err := store.ApplyGap(context.Background(), execution.GapGuardApplyRequest{Contract: frozenRef(), Items: []execution.PlanGapMutation{opened}})
	if err != nil || apply.Items[0].Status != execution.GapGuardApplied {
		t.Fatalf("ApplyGap(open) = (%+v, %v)", apply, err)
	}
	conflicting := opened
	conflicting.ExpectedMarkerRevision = 1
	conflicting.MutationDigest = ""
	conflicting.Scopes[0].ReasonCode = execution.ReasonCode(contract.ReasonConfigDrift)
	conflicting, err = execution.BuildPlanGapMutation(conflicting)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := PlanGapKeyV2("alarmd", identity)
	original := append([]byte(nil), backend.values[key]...)
	apply, err = store.ApplyGap(context.Background(), execution.GapGuardApplyRequest{Contract: frozenRef(), Items: []execution.PlanGapMutation{conflicting}})
	if err != nil || apply.Items[0].Status != execution.GapGuardConflict || string(backend.values[key]) != string(original) {
		t.Fatalf("ApplyGap(same version, different digest) = (%+v, %v)", apply, err)
	}
	loaded, err := store.LoadGaps(context.Background(), execution.GapLoadRequest{Contract: frozenRef(), Items: []execution.PlanGapLoadItem{{Identity: identity, ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1"}}})
	if err != nil || loaded.Items[0].Status != execution.GapFound || len(loaded.Items[0].Scopes) != 1 {
		t.Fatalf("LoadGaps() = (%+v, %v)", loaded, err)
	}
	warmVersion := execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 120, SlotDigest: "slot-2"}
	warmed, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{Identity: identity, ExpectedMarkerRevision: 1,
		ApplyVersion: warmVersion, ScheduleRevision: "plan-r1", Scopes: []execution.GapScopeMutation{{Scope: execution.GapScope{LevelID: 1, HasLevel: true}, Kind: execution.GapWarmup, ReasonCode: execution.ReasonCode(contract.ReasonHistoryGapped), RequiredFullSlots: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	apply, err = store.ApplyGap(context.Background(), execution.GapGuardApplyRequest{Contract: frozenRef(), Items: []execution.PlanGapMutation{warmed}})
	if err != nil || apply.Items[0].Status != execution.GapGuardApplied {
		t.Fatalf("ApplyGap(warmup) = (%+v, %v)", apply, err)
	}
	loaded, err = store.LoadGaps(context.Background(), execution.GapLoadRequest{Contract: frozenRef(), Items: []execution.PlanGapLoadItem{{Identity: identity, ApplyVersion: warmVersion, ScheduleRevision: "plan-r1"}}})
	if err != nil || loaded.Items[0].Scopes[0].ObservedFullSlots != 1 {
		t.Fatalf("LoadGaps(warmup) = (%+v, %v)", loaded, err)
	}
	clearVersion := execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 180, SlotDigest: "slot-3"}
	cleared, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{Identity: identity, ExpectedMarkerRevision: 2,
		ApplyVersion: clearVersion, ScheduleRevision: "plan-r1", Scopes: []execution.GapScopeMutation{{Scope: execution.GapScope{LevelID: 1, HasLevel: true}, Kind: execution.GapClear}}})
	if err != nil {
		t.Fatal(err)
	}
	apply, err = store.ApplyGap(context.Background(), execution.GapGuardApplyRequest{Contract: frozenRef(), Items: []execution.PlanGapMutation{cleared}})
	if err != nil || apply.Items[0].Status != execution.GapGuardApplied {
		t.Fatalf("ApplyGap(clear) = (%+v, %v)", apply, err)
	}
	loaded, err = store.LoadGaps(context.Background(), execution.GapLoadRequest{Contract: frozenRef(), Items: []execution.PlanGapLoadItem{{Identity: identity, ApplyVersion: clearVersion, ScheduleRevision: "plan-r1"}}})
	if err != nil || loaded.Items[0].Status != execution.GapClearedTombstone {
		t.Fatalf("LoadGaps(tombstone) = (%+v, %v)", loaded, err)
	}
}

func TestGapOversizeIsLocalAndApplyDoesNotOverwrite(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	router, _ := NewFixedRouter("monitor-01", backend)
	good := execution.PlanGapIdentity{Plan: stateIdentityV2().Plan, StateGeneration: "good"}
	bad := execution.PlanGapIdentity{Plan: stateIdentityV2().Plan, StateGeneration: "bad"}
	mutation, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{Identity: good, ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1", Scopes: []execution.GapScopeMutation{{Kind: execution.GapOpen, ReasonCode: execution.ReasonCode(contract.ReasonHistoryGapped), RequiredFullSlots: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	goodRaw, _ := json.Marshal(gapEnvelope{Schema: executionGapSchemaV2, Identity: good, MarkerRevision: 1, ApplyVersion: mutation.ApplyVersion, MutationDigest: mutation.MutationDigest, ScheduleRevision: mutation.ScheduleRevision, Scopes: []execution.GapScopeState{{Status: execution.GapStatusGapped, ReasonCode: execution.ReasonCode(contract.ReasonHistoryGapped), RequiredFullSlots: 2}}})
	limit := len(goodRaw) + 16
	goodKey, _ := PlanGapKeyV2("alarmd", good)
	badKey, _ := PlanGapKeyV2("alarmd", bad)
	backend.values[goodKey], backend.values[badKey] = goodRaw, []byte(strings.Repeat("x", limit+1))
	store, _ := NewExecutionStore(ExecutionStoreOptions{Prefix: "alarmd", Router: router, MaxValueBytes: limit, MaxItemsPerCall: 4, RuntimeTTL: time.Hour})
	loaded, err := store.LoadGaps(context.Background(), execution.GapLoadRequest{Contract: frozenRef(), Items: []execution.PlanGapLoadItem{{Identity: good, ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1"}, {Identity: bad, ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1"}}})
	if err != nil || loaded.Items[0].Status != execution.GapFound || loaded.Items[1].Status != execution.GapTerminal {
		t.Fatalf("LoadGaps(mixed) = (%+v, %v)", loaded, err)
	}
	badMutation := mutation
	badMutation.Identity, badMutation.MutationDigest = bad, ""
	badMutation, err = execution.BuildPlanGapMutation(badMutation)
	if err != nil {
		t.Fatal(err)
	}
	original := append([]byte(nil), backend.values[badKey]...)
	applied, err := store.ApplyGap(context.Background(), execution.GapGuardApplyRequest{Contract: frozenRef(), Items: []execution.PlanGapMutation{badMutation}})
	if err != nil || applied.Items[0].Status != execution.GapGuardRejected || string(backend.values[badKey]) != string(original) {
		t.Fatalf("ApplyGap(oversize) = (%+v, %v)", applied, err)
	}
}

func frozenRef() execution.FrozenExecutionContractRef {
	return execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "q", EvaluationTime: 60}, SnapshotRevision: "snapshot", QueryRevision: "query", ScheduleRevision: "schedule", ScheduleSegmentStart: 60, DuePlanSetDigest: "plans"}
}
func applyVersion() execution.ApplyVersion {
	return execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 60, SlotDigest: "slot"}
}
func stateIdentityV2() execution.StateKeyIdentity {
	return execution.StateKeyIdentity{Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "9"}, StateGeneration: "generation", SeriesIdentityDigest: "series"}
}
