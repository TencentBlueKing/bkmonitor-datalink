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
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func retentionEvery(points uint32, interval time.Duration) []execution.StateRetentionRequirement {
	return []execution.StateRetentionRequirement{{LevelID: 1, RetentionPoints: points, EvaluationInterval: interval}}
}

func planRetention(retention []execution.StateRetentionRequirement) execution.GenerationRetention {
	return execution.GenerationRetention{ByPlan: map[execution.PlanIdentity][]execution.StateRetentionRequirement{
		stateIdentityV2().Plan: retention,
	}}
}

// renewedTo is what the load renews a generation key of this retention to,
// under generationStore's bounds: the lifetime a write has to give it too.
func renewedTo(t *testing.T, retention []execution.StateRetentionRequirement) time.Duration {
	t.Helper()
	requirements := make([]LevelRequirement, len(retention))
	for index, level := range retention {
		requirements[index] = NewLevelRequirement(level, "", 0)
	}
	ttl, err := GenerationScopedTTL(requirements, time.Minute, time.Minute, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return ttl
}

func gapMarkerMutation(t *testing.T, evaluationTime int64, expected uint64) execution.PlanGapMutation {
	t.Helper()
	version := applyVersion()
	version.EvaluationTime = execution.EvaluationTime(evaluationTime)
	mutation, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{
		Identity:               execution.PlanGapIdentity{Plan: stateIdentityV2().Plan, StateGeneration: "generation"},
		ExpectedMarkerRevision: expected, ApplyVersion: version, ScheduleRevision: "plan-r1",
		Scopes: []execution.GapScopeMutation{{
			Kind: execution.GapOpen, ReasonCode: execution.ReasonCode("GAP_SKIPPED"), RequiredFullSlots: 1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return mutation
}

// A Plan's gap marker and no-data memory are written for the lifetime their
// load renews them to, so the write never takes back what the load gave and
// the key outlives the interval to the Plan's next round. A Plan on a
// sixty-hour interval used to lose both before its next round could read
// them; a Plan whose lifetime is the floor is written exactly as before.
func TestAWriteGivesAGenerationKeyTheLifetimeItsLoadRenewsItTo(t *testing.T) {
	for name, retention := range map[string][]execution.StateRetentionRequirement{
		"a one-minute Plan, whose lifetime is the floor": retentionEvery(5, time.Minute),
		"an hourly Plan past a day":                      retentionEvery(30, time.Hour),
		"a sixty-hour Plan past the ceiling":             retentionEvery(14, 60*time.Hour),
	} {
		t.Run(name, func(t *testing.T) {
			want := renewedTo(t, retention)
			if next := retention[0].EvaluationInterval + time.Minute; want < next {
				t.Fatalf("setup: the lifetime %s does not outlive the next round at %s", want, next)
			}
			if retention[0].EvaluationInterval == time.Minute && want != GenerationScopedFloor {
				t.Fatalf("setup: the one-minute Plan's lifetime is %s, want the floor", want)
			}

			gapBackend := &casMemoryBackend{values: make(map[string][]byte)}
			gapKey, err := PlanGapKeyV2("alarmd", execution.PlanGapIdentity{Plan: stateIdentityV2().Plan, StateGeneration: "generation"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := generationStore(t, gapBackend).ApplyGap(context.Background(), execution.GapGuardApplyRequest{
				Contract: frozenRef(), Items: []execution.PlanGapMutation{gapMarkerMutation(t, 60, 0)}, Retention: planRetention(retention),
			}); err != nil {
				t.Fatal(err)
			}
			if got := gapBackend.writeTTLs[gapKey]; got != want {
				t.Fatalf("gap marker written for %s, want %s, the lifetime its load renews it to", got, want)
			}

			noDataBackend := &casMemoryBackend{values: make(map[string][]byte)}
			noDataKey, err := PlanNoDataHashKeyV2("alarmd", noDataIdentityV2())
			if err != nil {
				t.Fatal(err)
			}
			result, err := generationStore(t, noDataBackend).ApplyNoData(context.Background(), execution.NoDataApplyRequest{
				Contract: frozenRef(), Items: []execution.PlanNoDataMutation{noDataMutationV2(t, 0, execution.NoDataGroupMemory{GroupKey: "a", FirstAbsent: 940})},
				Retention: planRetention(retention),
			})
			if err != nil || len(result.Items) != 1 || result.Items[0].Status != execution.NoDataApplied {
				t.Fatalf("no-data apply = %+v, %v", result.Items, err)
			}
			if got := noDataBackend.writeTTLs[noDataKey]; got != want {
				t.Fatalf("no-data memory written for %s, want %s, the lifetime its load renews it to", got, want)
			}
		})
	}
}

// After the load asks about a key, the gate does not ask again for a
// quarter of the key's lifetime, and a write in between is the key's last
// word on how long it lives. Written at the floor, a key whose lifetime is
// past 96 hours is gone before the next ask - a Plan loading every minute
// loses its marker a day after the write. Written for its own lifetime it
// outlives the silence on either side of that line.
func TestAKeyWrittenAfterAnAskOutlivesTheGatesSilence(t *testing.T) {
	for name, test := range map[string]struct {
		points      uint32
		pastTheLine bool
	}{
		"a lifetime under 96 hours": {points: 93, pastTheLine: false},
		"a lifetime over 96 hours":  {points: 100, pastTheLine: true},
	} {
		t.Run(name, func(t *testing.T) {
			retention := retentionEvery(test.points, time.Hour)
			lifetime := renewedTo(t, retention)
			if (lifetime > 96*time.Hour) != test.pastTheLine {
				t.Fatalf("setup: lifetime %s is on the wrong side of 96 hours", lifetime)
			}
			backend := &casMemoryBackend{values: make(map[string][]byte)}
			store := generationStore(t, backend)
			clock := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
			store.renewals.now = func() time.Time { return clock }
			identity := execution.PlanGapIdentity{Plan: stateIdentityV2().Plan, StateGeneration: "generation"}
			key, err := PlanGapKeyV2("alarmd", identity)
			if err != nil {
				t.Fatal(err)
			}
			load := func() {
				t.Helper()
				if _, err := store.LoadGaps(context.Background(), execution.GapLoadRequest{Contract: frozenRef(), Items: []execution.PlanGapLoadItem{{
					Identity: identity, ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1", Retention: retention,
				}}}); err != nil {
					t.Fatal(err)
				}
			}
			write := func(evaluationTime int64, expected uint64) {
				t.Helper()
				result, err := store.ApplyGap(context.Background(), execution.GapGuardApplyRequest{
					Contract: frozenRef(), Items: []execution.PlanGapMutation{gapMarkerMutation(t, evaluationTime, expected)},
					Retention: planRetention(retention),
				})
				if err != nil || result.Items[0].Status != execution.GapGuardApplied {
					t.Fatalf("gap apply = %+v, %v", result.Items, err)
				}
			}

			write(60, 0)
			load()
			asks := len(backend.renewals)
			if asks != 1 {
				t.Fatalf("setup: the first load asked %d times, want once", asks)
			}
			clock = clock.Add(time.Minute)
			write(120, 1)
			writtenAt, written := clock, backend.writeTTLs[key]
			// Every minute until the gate asks again.
			for len(backend.renewals) == asks {
				clock = clock.Add(time.Minute)
				if clock.Sub(writtenAt) > 30*24*time.Hour {
					t.Fatal("the gate never asked again")
				}
				load()
			}
			silence := clock.Sub(writtenAt)
			if written < silence {
				t.Fatalf("the marker written for %s expired before the gate asked again %s later", written, silence)
			}
			// The floor, as the write used to give it, outlives the silence
			// exactly when the lifetime is under the line.
			if floorSurvives := GenerationScopedFloor >= silence; floorSurvives == test.pastTheLine {
				t.Fatalf("setup: the floor survives a %s silence = %v, want %v", silence, floorSurvives, !test.pastTheLine)
			}
		})
	}
}

// A write that does not say what its Plan's keys live for is refused whole,
// on both paths, rather than written at a guess; a writer that says it has no
// compiled Plan writes at the ceiling, which the next evaluated round's write
// takes back to the Plan's own lifetime.
func TestAGenerationWriteWithoutItsPlansRetentionIsRefused(t *testing.T) {
	store := generationStore(t, &casMemoryBackend{values: make(map[string][]byte)})
	_, gapErr := store.ApplyGap(context.Background(), execution.GapGuardApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanGapMutation{gapMarkerMutation(t, 60, 0)},
	})
	_, noDataErr := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataMutation{noDataMutationV2(t, 0, execution.NoDataGroupMemory{GroupKey: "a", FirstAbsent: 940})},
		Retention: execution.GenerationRetention{ByPlan: map[execution.PlanIdentity][]execution.StateRetentionRequirement{
			{TenantID: "tenant", BusinessID: "2", StrategyID: "another"}: retentionEvery(5, time.Minute),
		}},
	})
	for name, err := range map[string]error{"gap marker": gapErr, "no-data memory": noDataErr} {
		if err == nil || !strings.Contains(err.Error(), "carries no retention") {
			t.Fatalf("%s write without its Plan's retention: error %v, want a refusal", name, err)
		}
	}

	backend := &casMemoryBackend{values: make(map[string][]byte)}
	key, err := PlanGapKeyV2("alarmd", execution.PlanGapIdentity{Plan: stateIdentityV2().Plan, StateGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generationStore(t, backend).ApplyGap(context.Background(), execution.GapGuardApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanGapMutation{gapMarkerMutation(t, 60, 0)},
		Retention: execution.GenerationRetention{Unknown: true},
	}); err != nil {
		t.Fatal(err)
	}
	ceiling := 30 * 24 * time.Hour
	if got := backend.writeTTLs[key]; got != ceiling {
		t.Fatalf("a marker written without a compiled Plan lives %s, want the ceiling %s", got, ceiling)
	}

	// The next round that evaluates the Plan writes it for the Plan's own
	// lifetime: the ceiling stays only on a marker nothing evaluates after.
	retention := retentionEvery(30, time.Hour)
	want := renewedTo(t, retention)
	if want <= GenerationScopedFloor || want >= ceiling {
		t.Fatalf("setup: the Plan's lifetime %s is not between the floor and the ceiling", want)
	}
	result, err := generationStore(t, backend).ApplyGap(context.Background(), execution.GapGuardApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanGapMutation{gapMarkerMutation(t, 120, 1)}, Retention: planRetention(retention),
	})
	if err != nil || result.Items[0].Status != execution.GapGuardApplied {
		t.Fatalf("the evaluated round's write = %+v, %v", result.Items, err)
	}
	if got := backend.writeTTLs[key]; got != want {
		t.Fatalf("the marker the next evaluated round wrote lives %s, want the Plan's own %s", got, want)
	}
}

// A store whose ceiling is under a day still writes a marker without a
// compiled Plan for the floor: no generation-scoped key lives less.
func TestAWriteWithoutACompiledPlanNeverGoesBelowTheFloor(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	router, err := NewFixedRouter("target", backend)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewExecutionStore(ExecutionStoreOptions{
		Prefix: "alarmd", Router: router, MaxValueBytes: 4096, MaxItemsPerCall: 4,
		MinTTL: time.Minute, MaxTTL: time.Hour, RestartMargin: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	key, err := PlanGapKeyV2("alarmd", execution.PlanGapIdentity{Plan: stateIdentityV2().Plan, StateGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyGap(context.Background(), execution.GapGuardApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanGapMutation{gapMarkerMutation(t, 60, 0)},
		Retention: execution.GenerationRetention{Unknown: true},
	}); err != nil {
		t.Fatal(err)
	}
	if got := backend.writeTTLs[key]; got != GenerationScopedFloor {
		t.Fatalf("a marker written without a compiled Plan under a one-hour ceiling lives %s, want the floor", got)
	}
}
