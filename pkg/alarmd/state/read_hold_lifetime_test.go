// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestReadHoldExtendsLifetimeButNotRetainedWindow(t *testing.T) {
	retention := ttlTestRetention(1, time.Minute, 5*time.Second)
	requirement := NewLevelRequirement(retention[0], "level", 1)
	before := retentionCutoff(3600, requirement)
	base, err := StateTTL([]LevelRequirement{requirement}, time.Minute, time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	const bound = 10 * time.Minute
	held, err := StateTTL([]LevelRequirement{requirement}, time.Minute, time.Minute, time.Hour, bound)
	if err != nil {
		t.Fatal(err)
	}
	if held != base+bound || retentionCutoff(3600, requirement) != before || requirement.LatenessTolerance != 5*time.Second {
		t.Fatalf("base=%s held=%s cutoff=%d", base, held, retentionCutoff(3600, requirement))
	}
	if StateLifetimeFloor([]LevelRequirement{requirement}, time.Minute, bound) != StateLifetimeFloor([]LevelRequirement{requirement}, time.Minute)+bound {
		t.Fatal("lifetime floor lost the hold")
	}
	store, backend, _, _ := frozenRenewalFixture(t)
	store.options.ReadHoldBound = bound
	got, err := store.runtimeTTL(retention, 30)
	if err != nil {
		t.Fatal(err)
	}
	if want := 2*time.Minute + 5*time.Second + bound; got != want {
		t.Fatalf("horizon floored lifetime=%s, want %s", got, want)
	}
	// A state written before the hold jumps must survive the jump and the
	// maximum-age replay. The write always takes the bound, not today's h.
	mutation, err := execution.BuildStateMutation(execution.StateMutation{Identity: stateIdentityV2(), ApplyVersion: execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 120, SlotDigest: "slot"}, ExpectedBlobRevision: 1,
		AffectedRecords: []execution.RecordAnchor{derivedAnchor(t, stateIdentityV2(), 120)},
		Levels:          []execution.RuntimeLevelStateMutation{{LevelID: 1, LevelStateCompatibility: "compat", HistoryCompleteness: execution.HistoryFull, WarmupRequirementRef: "warm", LastProcessedEventTime: 120}},
		Points:          []execution.StateHistoryPoint{derivedPoint(t, stateIdentityV2(), 120, "detect", execution.LevelFactNormal)}})
	if err != nil {
		t.Fatal(err)
	}
	backend.writeTTLs = nil
	result, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{Contract: frozenRef(), Retention: retention, Items: []execution.StateMutation{mutation}, HorizonSeconds: 30})
	if err != nil || result.Items[0].Status != execution.StateApplied {
		t.Fatalf("write=%+v %v", result, err)
	}
	for _, ttl := range backend.writeTTLs {
		if ttl != got {
			t.Fatalf("written lifetime %s != %s", ttl, got)
		}
	}
	if len(backend.writeTTLs) == 0 {
		t.Fatal("no state lifetime was checked")
	}
}

type holdAgeBackend struct {
	*casMemoryBackend
	elapsed   time.Duration
	deadlines map[string]time.Duration
}

func (backend *holdAgeBackend) CompareAndSet(ctx context.Context, key string, expected []byte, missing bool, value []byte, ttl time.Duration) (bool, error) {
	applied, err := backend.casMemoryBackend.CompareAndSet(ctx, key, expected, missing, value, ttl)
	if applied {
		backend.deadlines[key] = backend.elapsed + ttl
	}
	return applied, err
}

func (backend *holdAgeBackend) MGet(ctx context.Context, keys []string) ([][]byte, error) {
	for _, key := range keys {
		if deadline, found := backend.deadlines[key]; found && backend.elapsed >= deadline {
			delete(backend.values, key)
		}
	}
	return backend.casMemoryBackend.MGet(ctx, keys)
}

func TestStateWrittenBeforeHoldJumpSurvivesTheNextHeldRead(t *testing.T) {
	const bound = 10 * time.Minute
	for _, arm := range []struct {
		name  string
		bound time.Duration
		want  execution.StateLoadStatus
	}{
		{name: "without hold lifetime", want: execution.StateMissingWarming},
		{name: "hold bound from the first write", bound: bound, want: execution.StateFoundReady},
	} {
		t.Run(arm.name, func(t *testing.T) {
			backend := &holdAgeBackend{casMemoryBackend: &casMemoryBackend{values: make(map[string][]byte)}, deadlines: make(map[string]time.Duration)}
			router, _ := NewFixedRouter("target", backend)
			store, err := NewExecutionStore(ExecutionStoreOptions{Prefix: "alarmd", Router: router, MaxValueBytes: 4096, MaxItemsPerCall: 4,
				MinTTL: time.Minute, MaxTTL: time.Hour, RestartMargin: time.Minute, ReadHoldBound: arm.bound})
			if err != nil {
				t.Fatal(err)
			}
			mutation, err := execution.BuildStateMutation(execution.StateMutation{Identity: stateIdentityV2(), ApplyVersion: applyVersion(),
				AffectedRecords: []execution.RecordAnchor{derivedAnchor(t, stateIdentityV2(), 60)},
				Levels: []execution.RuntimeLevelStateMutation{{LevelID: 1, LevelStateCompatibility: "compat", HistoryCompleteness: execution.HistoryFull,
					WarmupRequirementRef: "warm", LastProcessedEventTime: 60}},
				Points: []execution.StateHistoryPoint{derivedPoint(t, stateIdentityV2(), 60, "detect", execution.LevelFactNormal)}})
			if err != nil {
				t.Fatal(err)
			}
			retention := ttlTestRetention(1, time.Minute, 5*time.Second)
			if result, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{Contract: frozenRef(), Retention: retention, Items: []execution.StateMutation{mutation}, HorizonSeconds: 30}); err != nil || result.Items[0].Status != execution.StateApplied {
				t.Fatalf("write before jump = %+v %v", result, err)
			}
			// h was zero on the first write. An immediate jump to its global
			// bound moves the next read one interval plus H after that write.
			backend.elapsed = time.Minute + bound
			heldContract := frozenRef()
			heldContract.Slot.EvaluationTime, heldContract.ReadHoldMillis = 120, bound.Milliseconds()
			version := applyVersion()
			version.EvaluationTime, version.SlotDigest = 120, "held-slot"
			loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: heldContract,
				Items: []execution.StatePreflightItem{{Identity: stateIdentityV2(), ApplyVersion: version}}})
			if err != nil || loaded.Items[0].Status != arm.want {
				t.Fatalf("read after hold jump = %+v %v; want %s", loaded, err, arm.want)
			}
		})
	}
}
