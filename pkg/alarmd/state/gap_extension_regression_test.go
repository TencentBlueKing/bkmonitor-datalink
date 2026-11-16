// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestSameSlotGapExtensionPreservesScopesAndChecksCAS(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(map[bool]string{false: "extension", true: "concurrent writer"}[concurrent], func(t *testing.T) {
			backend := &casMemoryBackend{values: make(map[string][]byte)}
			router, _ := NewFixedRouter("test", backend)
			store, err := NewExecutionStore(ExecutionStoreOptions{Prefix: "alarmd", Router: router, MaxValueBytes: 4096, MaxItemsPerCall: 4, MinTTL: time.Minute, MaxTTL: time.Hour, RestartMargin: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			identity := execution.PlanGapIdentity{Plan: stateIdentityV2().Plan, StateGeneration: "generation"}
			build := func(scopes []execution.GapScopeMutation, rev uint64) execution.PlanGapMutation {
				m, e := execution.BuildPlanGapMutation(execution.PlanGapMutation{Identity: identity, ExpectedMarkerRevision: rev, ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1", Scopes: scopes})
				if e != nil {
					t.Fatal(e)
				}
				return m
			}
			level := execution.GapScopeMutation{Scope: execution.GapScope{HasLevel: true, LevelID: 1}, Kind: execution.GapOpen, ReasonCode: execution.ReasonCode(contract.ReasonHistoryGapped), RequiredFullSlots: 3}
			otherLevel := level
			otherLevel.Scope.LevelID = 2
			otherLevel.RequiredFullSlots = 4
			old := build([]execution.GapScopeMutation{level, otherLevel}, 0)
			apply := func(m execution.PlanGapMutation) execution.GapGuardApplyStatus {
				r, e := store.ApplyGap(context.Background(), execution.GapGuardApplyRequest{Retention: execution.GenerationRetention{Unknown: true}, Contract: frozenRef(), Items: []execution.PlanGapMutation{m}})
				if e != nil {
					t.Fatal(e)
				}
				return r.Items[0].Status
			}
			if s := apply(old); s != execution.GapGuardApplied {
				t.Fatalf("seed=%s", s)
			}
			load := func() execution.GapGuardSnapshot {
				r, e := store.LoadGaps(context.Background(), execution.GapLoadRequest{Contract: frozenRef(), Items: []execution.PlanGapLoadItem{{Identity: identity, ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1"}}})
				if e != nil {
					t.Fatal(e)
				}
				return r.Items[0]
			}
			before := load()
			extend := build([]execution.GapScopeMutation{{Scope: execution.GapScope{}, Kind: execution.GapOpen, ReasonCode: execution.ReasonCode(contract.ReasonSnapshotUnavailable), RequiredFullSlots: 9}}, before.MarkerRevision)
			staleRevision := extend
			staleRevision.ExpectedMarkerRevision = 0
			if s := apply(staleRevision); s != execution.GapGuardConflict || !reflect.DeepEqual(before, load()) {
				t.Fatal("extension bypassed marker revision")
			}
			weaken := level
			weaken.RequiredFullSlots--
			if s := apply(build([]execution.GapScopeMutation{weaken}, before.MarkerRevision)); s != execution.GapGuardConflict || !reflect.DeepEqual(before, load()) {
				t.Fatal("same-Slot weakening was accepted")
			}
			wrongSchedule := extend
			wrongSchedule.ScheduleRevision = "plan-r2"
			wrongSchedule.MutationDigest = ""
			wrongSchedule, err = execution.BuildPlanGapMutation(wrongSchedule)
			if err != nil {
				t.Fatal(err)
			}
			if s := apply(wrongSchedule); s != execution.GapGuardConflict || !reflect.DeepEqual(before, load()) {
				t.Fatal("extension crossed schedule revision")
			}
			backend.conflict = concurrent
			got := apply(extend)
			if concurrent {
				if got != execution.GapGuardConflict || !reflect.DeepEqual(before, load()) {
					t.Fatal("CAS conflict did not preserve marker")
				}
				return
			}
			if got != execution.GapGuardApplied {
				t.Fatalf("same Slot extension=%s, want APPLIED", got)
			}
			after := load()
			if after.MarkerRevision != before.MarkerRevision+1 || len(after.Scopes) != 3 {
				t.Fatalf("extension=%+v", after)
			}
			for _, original := range before.Scopes {
				var found bool
				for _, s := range after.Scopes {
					if s.Scope == original.Scope {
						found = true
						if s != original {
							t.Fatalf("level scope changed: %+v", s)
						}
					}
				}
				if !found {
					t.Fatal("existing scope lost")
				}
			}
			if s := apply(extend); s != execution.GapGuardAlreadyApplied {
				t.Fatalf("redo=%s", s)
			}
		})
	}
}

// The same-Slot extension contract preserves observations, unlike a new-Slot
// strengthen. Do not reject existing GAPPED markers merely for having them.
func TestSameSlotStrengtheningPersistsExistingObservations(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	router, _ := NewFixedRouter("test", backend)
	store, err := NewExecutionStore(ExecutionStoreOptions{Prefix: "alarmd", Router: router, MaxValueBytes: 4096, MaxItemsPerCall: 4, MinTTL: time.Minute, MaxTTL: time.Hour, RestartMargin: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	identity := execution.PlanGapIdentity{Plan: stateIdentityV2().Plan, StateGeneration: "generation"}
	version := applyVersion()
	apply := func(kind execution.GapMutationKind, revision uint64) {
		mutation, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{
			Identity: identity, ExpectedMarkerRevision: revision, ApplyVersion: version, ScheduleRevision: "plan-r1",
			Scopes: []execution.GapScopeMutation{{Kind: kind, ReasonCode: execution.ReasonCode(contract.ReasonHistoryGapped), RequiredFullSlots: 9}},
		})
		if err != nil {
			t.Fatal(err)
		}
		result, err := store.ApplyGap(context.Background(), execution.GapGuardApplyRequest{Retention: execution.GenerationRetention{Unknown: true}, Contract: frozenRef(), Items: []execution.PlanGapMutation{mutation}})
		if err != nil || result.Items[0].Status != execution.GapGuardApplied {
			t.Fatalf("%s: result=%+v err=%v", kind, result, err)
		}
	}
	load := func() execution.GapGuardSnapshot {
		result, err := store.LoadGaps(context.Background(), execution.GapLoadRequest{Contract: frozenRef(), Items: []execution.PlanGapLoadItem{{Identity: identity, ApplyVersion: version, ScheduleRevision: "plan-r1"}}})
		if err != nil {
			t.Fatal(err)
		}
		return result.Items[0]
	}
	apply(execution.GapOpen, 0)
	version.EvaluationTime += 60
	apply(execution.GapWarmup, load().MarkerRevision)
	before := load()
	if before.Status != execution.GapFound || len(before.Scopes) != 1 || before.Scopes[0].Status != execution.GapStatusWarming || before.Scopes[0].ObservedFullSlots != 1 {
		t.Fatalf("warmup=%+v", before)
	}
	apply(execution.GapStrengthen, before.MarkerRevision)
	after := load()
	want := before.Scopes[0]
	want.Status = execution.GapStatusGapped
	if after.Status != execution.GapFound || after.MarkerRevision != before.MarkerRevision+1 || len(after.Scopes) != 1 || after.Scopes[0] != want {
		t.Fatalf("same-Slot extension lost observations: %+v", after)
	}
}

// Within one Slot a scope's reason only rises: a statement at the marker's
// own version that answers a held PARTIAL scope UNAVAILABLE is applied and
// the marker says UNAVAILABLE; one that answers a held UNAVAILABLE scope
// PARTIAL changes nothing and is answered as no growth (CONFLICT), the
// marker left as it was.
func TestASameSlotScopesReasonOnlyRises(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	router, _ := NewFixedRouter("test", backend)
	store, err := NewExecutionStore(ExecutionStoreOptions{Prefix: "alarmd", Router: router, MaxValueBytes: 4096, MaxItemsPerCall: 4, MinTTL: time.Minute, MaxTTL: time.Hour, RestartMargin: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	identity := execution.PlanGapIdentity{Plan: stateIdentityV2().Plan, StateGeneration: "generation"}
	level := func(reason string) execution.GapScopeMutation {
		return execution.GapScopeMutation{Scope: execution.GapScope{HasLevel: true, LevelID: 1}, Kind: execution.GapOpen,
			ReasonCode: execution.ReasonCode(reason), RequiredFullSlots: 3}
	}
	build := func(scope execution.GapScopeMutation, revision uint64) execution.PlanGapMutation {
		mutation, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{Identity: identity, ExpectedMarkerRevision: revision,
			ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1", Scopes: []execution.GapScopeMutation{scope}})
		if err != nil {
			t.Fatal(err)
		}
		return mutation
	}
	apply := func(mutation execution.PlanGapMutation) execution.GapGuardApplyStatus {
		result, err := store.ApplyGap(context.Background(), execution.GapGuardApplyRequest{Retention: execution.GenerationRetention{Unknown: true},
			Contract: frozenRef(), Items: []execution.PlanGapMutation{mutation}})
		if err != nil {
			t.Fatal(err)
		}
		return result.Items[0].Status
	}
	load := func() execution.GapGuardSnapshot {
		result, err := store.LoadGaps(context.Background(), execution.GapLoadRequest{Contract: frozenRef(),
			Items: []execution.PlanGapLoadItem{{Identity: identity, ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1"}}})
		if err != nil {
			t.Fatal(err)
		}
		return result.Items[0]
	}
	if status := apply(build(level(contract.ReasonQueryPartial), 0)); status != execution.GapGuardApplied {
		t.Fatalf("seed = %s", status)
	}
	worse := load()
	if status := apply(build(level(contract.ReasonQueryUnavailable), worse.MarkerRevision)); status != execution.GapGuardApplied {
		t.Fatalf("a held PARTIAL scope answered UNAVAILABLE = %s, want applied", status)
	}
	raised := load()
	if len(raised.Scopes) != 1 || raised.Scopes[0].ReasonCode != execution.ReasonCode(contract.ReasonQueryUnavailable) ||
		raised.MarkerRevision != worse.MarkerRevision+1 {
		t.Fatalf("marker after the worse answer = %+v, want its scope UNAVAILABLE one revision on", raised)
	}
	if status := apply(build(level(contract.ReasonQueryPartial), raised.MarkerRevision)); status != execution.GapGuardConflict {
		t.Fatalf("a held UNAVAILABLE scope answered PARTIAL = %s, want no growth", status)
	}
	if after := load(); !reflect.DeepEqual(after.Scopes, raised.Scopes) || after.MarkerRevision != raised.MarkerRevision {
		t.Fatalf("marker after the better answer = %+v, want it as it was", after)
	}
}
