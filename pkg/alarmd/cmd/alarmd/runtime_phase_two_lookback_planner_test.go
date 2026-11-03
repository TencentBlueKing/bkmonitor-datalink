// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// plannerFleet is a real fleet store with two Workers whose lookback read
// late Query Groups have been published.
func plannerFleet(t *testing.T, now time.Time) (*fleet.RedisStore, *redis.Client) {
	t.Helper()
	_, client := startPhaseTwoRedis(t)
	store, err := fleet.NewRedisStore(client, "test.fleet", time.Minute, 0)
	if err != nil {
		t.Fatal(err)
	}
	for replica, late := range map[string]map[string]int{"w1": {"qg-1": 2}, "w2": {"qg-2": 4}} {
		snapshot := fleet.Snapshot{Replica: replica, TakenAt: now, LookbackLate: late}
		if _, err := store.PublishSummarized(context.Background(), snapshot, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	return store, client
}

// replicaDigest is how the fleet store names a replica in its keys.
func replicaDigest(replica string) string {
	digest := sha256.Sum256([]byte(replica))
	return hex.EncodeToString(digest[:])
}

var plannerOwners = map[execution.QueryGroupIdentity]string{"qg-1": "w2", "qg-2": "w2", "qg-3": "w1"}

// The preferred Leader plans every late Query Group the Workers report for
// whichever Worker owns it now - qg-1 was reported by w1 and moved to w2 -
// keeps the set, to live as long as its latest entry, and sends the Worker
// that owns none of them the empty set. A set that did not change is not
// written again.
func TestThePreferredLeaderPlansTheLateQueryGroupsForTheirOwnersAndKeepsTheSet(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store, client := plannerFleet(t, now)
	planner := &lookbackPlanner{preferred: true, store: store, now: func() time.Time { return now }}
	byWorker, err := planner.plan(context.Background(), plannerOwners, []string{"w1", "w2"})
	if err != nil {
		t.Fatal(err)
	}
	if got := byWorker["w2"]; len(got) != 2 || got[0].QueryGroup != "qg-1" || got[0].Rung != 2 || got[1].Rung != 4 ||
		!got[0].ExpiresAt.Equal(now.Add(lookback.PlanLife)) {
		t.Fatalf("w2's plans = %+v, want qg-1 at rung 2 and qg-2 at rung 4 for the plan's life", got)
	}
	if got, present := byWorker["w1"]; !present || len(got) != 0 {
		t.Fatalf("w1's plans = %+v (present %t), want the empty set", got, present)
	}
	kept, err := store.LoadLookbackPlan(context.Background(), now)
	if err != nil || len(kept) != 2 || kept["qg-2"].Rung != 4 {
		t.Fatalf("kept set %+v error %v, want both entries", kept, err)
	}
	if ttl := client.PTTL(context.Background(), "test.fleet:lookback-plan").Val(); ttl <= lookback.PlanLife-time.Second || ttl > lookback.PlanLife {
		t.Fatalf("kept set ttl %v, want the latest entry's life %v", ttl, lookback.PlanLife)
	}
	before := client.Get(context.Background(), "test.fleet:lookback-plan").Val()
	now = now.Add(time.Minute)
	if _, err := planner.plan(context.Background(), plannerOwners, []string{"w1", "w2"}); err != nil {
		t.Fatal(err)
	}
	if after := client.Get(context.Background(), "test.fleet:lookback-plan").Val(); after != before {
		t.Fatalf("an unchanged set was written again:\n%s\n%s", before, after)
	}
}

// A fallback Leader plans nothing: it neither reads nor writes the kept set,
// sends nothing, and counts the round it paused. Elected after a gap longer
// than an entry's life, a preferred Leader starts from an empty set; elected
// within it, from the set the last preferred Leader kept, whole.
func TestAFallbackLeavesTheKeptSetAndANewTermStartsFromItUnlessItExpired(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store, client := plannerFleet(t, now)
	first := &lookbackPlanner{preferred: true, store: store, now: func() time.Time { return now }}
	if _, err := first.plan(context.Background(), plannerOwners, []string{"w1", "w2"}); err != nil {
		t.Fatal(err)
	}
	kept := client.Get(context.Background(), "test.fleet:lookback-plan").Val()

	recorder := metric.NewRecorder(metric.BuildInfo{})
	fallback := &lookbackPlanner{preferred: false, store: store, recorder: recorder, now: func() time.Time { return now }}
	byWorker, err := fallback.plan(context.Background(), plannerOwners, []string{"w1", "w2"})
	if err != nil || byWorker != nil {
		t.Fatalf("a fallback's plans = %+v error %v, want none", byWorker, err)
	}
	if after := client.Get(context.Background(), "test.fleet:lookback-plan").Val(); after != kept {
		t.Fatalf("a fallback term changed the kept set:\n%s\n%s", kept, after)
	}
	if got := counterValue(t, recorder, "alarmd_control_tier_paused_total", map[string]string{"function": metric.ControlTierLookbackPlanning}); got != 1 {
		t.Fatalf("paused rounds = %v, want 1", got)
	}

	// Handed back within the life: a new preferred process, its Workers'
	// lists not yet published again, plans the kept set whole.
	_ = client.Del(context.Background(), "test.fleet:fleet-lookback:"+replicaDigest("w1"), "test.fleet:fleet-lookback:"+replicaDigest("w2"))
	back := &lookbackPlanner{preferred: true, store: store, now: func() time.Time { return now.Add(10 * time.Minute) }}
	byWorker, err = back.plan(context.Background(), plannerOwners, []string{"w1", "w2"})
	if err != nil || len(byWorker["w2"]) != 2 {
		t.Fatalf("after the handback w2's plans = %+v error %v, want the kept set whole", byWorker["w2"], err)
	}

	// Elected after the life ran out: nothing to start from.
	late := &lookbackPlanner{preferred: true, store: store, now: func() time.Time { return now.Add(lookback.PlanLife + time.Minute) }}
	byWorker, err = late.plan(context.Background(), plannerOwners, []string{"w1", "w2"})
	if err != nil || len(byWorker["w2"]) != 0 {
		t.Fatalf("after the life ran out w2's plans = %+v error %v, want none", byWorker["w2"], err)
	}
}

// Each term starts from the set as kept, not from what this process last
// planned: another Leader may have planned in between.
func TestEachTermStartsFromTheKeptSet(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store, _ := plannerFleet(t, now)
	planner := &lookbackPlanner{preferred: true, store: store, now: func() time.Time { return now }}
	if _, err := planner.plan(context.Background(), plannerOwners, []string{"w1", "w2"}); err != nil {
		t.Fatal(err)
	}
	other := map[string]fleet.LookbackPlanEntry{"qg-3": {Rung: 1, ExpiresAtMs: now.Add(time.Hour).UnixMilli()}}
	if err := store.SaveLookbackPlan(context.Background(), other, now); err != nil {
		t.Fatal(err)
	}
	planner.newTerm()
	byWorker, err := planner.plan(context.Background(), plannerOwners, []string{"w1", "w2"})
	if err != nil {
		t.Fatal(err)
	}
	if got := byWorker["w1"]; len(got) != 1 || got[0].QueryGroup != "qg-3" || got[0].Rung != 1 {
		t.Fatalf("w1's plans in the new term = %+v, want qg-3 from the kept set", got)
	}
}

// An entry no Worker reports any more leaves the set when its life runs
// out, and with the last one the kept key goes: a deployment whose late
// Query Groups recovered leaves nothing behind.
func TestAnEntryNobodyRenewsLeavesTheSetWithItsLife(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store, client := plannerFleet(t, now)
	planner := &lookbackPlanner{preferred: true, store: store, now: func() time.Time { return now }}
	if _, err := planner.plan(context.Background(), plannerOwners, []string{"w1", "w2"}); err != nil {
		t.Fatal(err)
	}
	_ = client.Del(context.Background(), "test.fleet:fleet-lookback:"+replicaDigest("w1"), "test.fleet:fleet-lookback:"+replicaDigest("w2"))
	now = now.Add(lookback.PlanLife + time.Second)
	byWorker, err := planner.plan(context.Background(), plannerOwners, []string{"w1", "w2"})
	if err != nil || len(byWorker["w2"]) != 0 {
		t.Fatalf("after the life with nothing reported w2's plans = %+v error %v, want none", byWorker["w2"], err)
	}
	if exists := client.Exists(context.Background(), "test.fleet:lookback-plan").Val(); exists != 0 {
		t.Fatal("the emptied set left its key behind")
	}
}

// failingPlanStore is a fleet store whose plan saves fail while failSaves
// is above zero, counting every save asked of it.
type failingPlanStore struct {
	*fleet.RedisStore
	failSaves int
	saves     int
}

func (store *failingPlanStore) SaveLookbackPlan(ctx context.Context, entries map[string]fleet.LookbackPlanEntry, now time.Time) error {
	store.saves++
	if store.failSaves > 0 {
		store.failSaves--
		return errors.New("store unavailable")
	}
	return store.RedisStore.SaveLookbackPlan(ctx, entries, now)
}

// A plan set the store refused is saved on the next round although nothing
// changed in between: the round that planned it already changed the set.
func TestAPlanSetTheStoreRefusedIsSavedOnTheNextRound(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	fleetStore, _ := plannerFleet(t, now)
	store := &failingPlanStore{RedisStore: fleetStore, failSaves: 1}
	planner := &lookbackPlanner{preferred: true, store: store, now: func() time.Time { return now }}
	if _, err := planner.plan(context.Background(), plannerOwners, []string{"w1", "w2"}); err == nil {
		t.Fatal("a round whose save failed reported no error")
	}
	now = now.Add(30 * time.Second)
	if _, err := planner.plan(context.Background(), plannerOwners, []string{"w1", "w2"}); err != nil {
		t.Fatal(err)
	}
	kept, err := fleetStore.LoadLookbackPlan(context.Background(), now)
	if store.saves != 2 || err != nil || len(kept) != 2 {
		t.Fatalf("saves %d, kept %v (error %v); want the identical round to save the set the failed one planned", store.saves, kept, err)
	}
}

// A Query Group that stays late is renewed once half its entry's life is
// gone: its entry runs a whole life from the renewal and the kept set is
// written again with it, so it never lapses while its Worker still reports it.
func TestAQueryGroupThatStaysLateIsRenewedAtHalfItsLife(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store, client := plannerFleet(t, now)
	planner := &lookbackPlanner{preferred: true, store: store, now: func() time.Time { return now }}
	if _, err := planner.plan(context.Background(), plannerOwners, []string{"w1", "w2"}); err != nil {
		t.Fatal(err)
	}
	before := client.Get(context.Background(), "test.fleet:lookback-plan").Val()
	// The lists live as long as the store's snapshots: publish them again,
	// as the Workers do, half a life on.
	now = now.Add(lookback.PlanLife/2 + time.Minute)
	for replica, late := range map[string]map[string]int{"w1": {"qg-1": 2}, "w2": {"qg-2": 4}} {
		if _, err := store.PublishSummarized(context.Background(), fleet.Snapshot{Replica: replica, TakenAt: now, LookbackLate: late}, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	byWorker, err := planner.plan(context.Background(), plannerOwners, []string{"w1", "w2"})
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range byWorker["w2"] {
		if !plan.ExpiresAt.Equal(now.Add(lookback.PlanLife)) {
			t.Fatalf("%s expires %s after its renewal at %s, want a whole life from it", plan.QueryGroup, plan.ExpiresAt, now)
		}
	}
	if after := client.Get(context.Background(), "test.fleet:lookback-plan").Val(); after == before {
		t.Fatal("the renewal was not written to the kept set")
	}
}

// A listed Query Group no Worker owns in the round's desired set - it left,
// or moves before the next round - goes into no Worker's set.
func TestALateQueryGroupNoWorkerOwnsIsPlannedForNobody(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store, _ := plannerFleet(t, now)
	planner := &lookbackPlanner{preferred: true, store: store, now: func() time.Time { return now }}
	owners := map[execution.QueryGroupIdentity]string{"qg-2": "w2"}
	byWorker, err := planner.plan(context.Background(), owners, []string{"w1", "w2"})
	if err != nil {
		t.Fatal(err)
	}
	for worker, plans := range byWorker {
		for _, plan := range plans {
			if plan.QueryGroup == "qg-1" {
				t.Fatalf("qg-1, owned by no Worker, is in %s's set", worker)
			}
		}
	}
	if len(byWorker["w2"]) != 1 {
		t.Fatalf("w2's set = %+v, want qg-2 alone", byWorker["w2"])
	}
}

// Starting a term on the view stream starts the planner's: the next round
// plans from the set as kept, not from what this process last planned.
func TestLeadingAViewStreamTermReloadsTheKeptPlanSet(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store, _ := plannerFleet(t, now)
	server, err := viewstream.NewServer(refuseEveryStream{}, nil, viewstream.ServerOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	runtime := &productionPhaseTwoOwnership{dependencies: productionPhaseTwoOwnershipDependencies{ViewStream: server}}
	runtime.WithLookbackPlanner(store, true, nil)
	runtime.lookbackPlanner.now = func() time.Time { return now }
	if _, err := runtime.lookbackPlanner.plan(context.Background(), plannerOwners, []string{"w1", "w2"}); err != nil {
		t.Fatal(err)
	}
	other := map[string]fleet.LookbackPlanEntry{"qg-3": {Rung: 1, ExpiresAtMs: now.Add(time.Hour).UnixMilli()}}
	if err := store.SaveLookbackPlan(context.Background(), other, now); err != nil {
		t.Fatal(err)
	}
	runtime.viewLead(9)
	byWorker, err := runtime.lookbackPlanner.plan(context.Background(), plannerOwners, []string{"w1", "w2"})
	if err != nil {
		t.Fatal(err)
	}
	if got := byWorker["w1"]; len(got) != 1 || got[0].QueryGroup != "qg-3" {
		t.Fatalf("w1's set after a new term = %+v, want qg-3 from the kept set", got)
	}
}

// refuseEveryStream admits no Worker: a server that only leads.
type refuseEveryStream struct{}

func (refuseEveryStream) Admit(context.Context, string, string) (string, error) { return "test", nil }

// Replicas that all read a Query Group directed on their own evidence, at
// different rungs - its old and new owner around a move, or more - give it
// the deepest rung, every round the same: the set is saved once, not again
// on every round the lists stand. Eight replicas, one of them deep: a round
// that took whichever list it read last would miss the deep one in most
// rounds, whatever order the lists come in.
func TestReplicasListingOneQueryGroupGiveItTheDeepestRungOnce(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	fleetStore, _ := plannerFleet(t, now)
	replicas := []string{"w1", "w2", "w3", "w4", "w5", "w6", "w7", "w8"}
	for _, replica := range replicas {
		rung := 1
		if replica == "w5" {
			rung = 4
		}
		if _, err := fleetStore.PublishSummarized(context.Background(), fleet.Snapshot{Replica: replica, TakenAt: now,
			LookbackLate: map[string]int{"qg-2": rung}}, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	store := &failingPlanStore{RedisStore: fleetStore}
	planner := &lookbackPlanner{preferred: true, store: store, now: func() time.Time { return now }}
	for round := 0; round < 20; round++ {
		byWorker, err := planner.plan(context.Background(), plannerOwners, replicas)
		if err != nil {
			t.Fatal(err)
		}
		for _, plan := range byWorker["w2"] {
			if plan.QueryGroup == "qg-2" && plan.Rung != 4 {
				t.Fatalf("round %d: qg-2 at rung %d, want the deepest 4", round, plan.Rung)
			}
		}
		now = now.Add(30 * time.Second)
	}
	if store.saves != 1 {
		t.Fatalf("saves over twenty rounds of the same lists = %d, want 1", store.saves)
	}
}
