// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

// cacheHeld is what one process's shared caches hold, read the way a scrape
// reads them.
type cacheHeld struct{ compiled, objects, compileClears, objectClears float64 }

func (fixture *electionFixture) cacheHeld(process *electionProcess) cacheHeld {
	fixture.t.Helper()
	entries := func(object string) float64 {
		return counterValue(fixture.t, process.recorder, "alarmd_control_cache_entries", map[string]string{"object": object})
	}
	clears := func(object string) float64 {
		return counterValue(fixture.t, process.recorder, "alarmd_control_cache_total",
			map[string]string{"object": object, "result": "clear"})
	}
	return cacheHeld{
		compiled: entries("plan_compile"), objects: entries("catalog_object"),
		compileClears: clears("plan_compile"), objectClears: clears("catalog_object"),
	}
}

// TestAFormerLeaderHoldsWhatAWorkerThatNeverLedHolds is the step-down of the
// split form with the caches in view. A Worker leads as the fallback and
// compiles and reads the catalog of every Query Group; a control process
// comes, and once the Worker has handed the lease back its compile cache and
// object cache hold what the other Worker's hold, which never led -- not the
// whole catalog -- while every Query Group keeps completing its Slots.
func TestAFormerLeaderHoldsWhatAWorkerThatNeverLedHolds(t *testing.T) {
	const queryGroups = 8
	fixture := newElectionFixture(t, queryGroups, "alarmd:phase-two:election-step-down-caches")
	former := fixture.open("caches-worker-1", roles.Set{roles.Worker}, true)
	never := fixture.open("caches-worker-2", roles.Set{roles.Worker}, true)
	if leader, _ := fixture.leader(); leader != former {
		t.Fatalf("leader with no control process = %v, want %s", leader, former.name)
	}
	fixture.balance(queryGroups)
	if got := fixture.pass(1, true, queryGroups); got != queryGroups {
		t.Fatalf("Slot 0 while a Worker leads: %d of %d Query Groups completed", got, queryGroups)
	}
	leading, other := fixture.cacheHeld(former), fixture.cacheHeld(never)
	if leading.compiled <= other.compiled || leading.objects <= other.objects {
		t.Fatalf("while leading the Worker's caches hold %+v, the other's %+v; the Leader's should hold more", leading, other)
	}

	control := fixture.open("caches-control", roles.Set{roles.Control}, true)
	fixture.tick()
	firstSeen := fixture.now()
	window := controlCandidacyWindow(control.bundle.dependencies.Config)
	for offset := 5 * time.Second; offset <= window+10*time.Second && fixture.transitions("preferred", "handback") == 0; offset += 5 * time.Second {
		fixture.clockMillis.Store(firstSeen.Add(offset).UnixMilli())
		fixture.awaitRenewals()
		fixture.tick()
	}
	if fixture.transitions("preferred", "handback") != 1 {
		t.Fatalf("handbacks = %v, want 1", fixture.transitions("preferred", "handback"))
	}
	fixture.advance(time.Second)
	fixture.tick()
	if leader, _ := fixture.leader(); leader != control {
		t.Fatalf("leader after the handback = %v, want %s", leader, control.name)
	}
	// Follower refresh ticks, which production's refresh ticker runs: the
	// first after the term clears, the rest do not, and the Worker that never
	// led is never cleared.
	for round := 0; round < 3; round++ {
		for _, worker := range []*electionProcess{former, never} {
			if err := worker.bundle.refreshAndReconcile(fixture.ctx, true); err != nil {
				t.Fatalf("%s follower refresh tick: %v", worker.name, err)
			}
		}
	}

	if got := fixture.pass(3, false, queryGroups); got != queryGroups {
		t.Fatalf("Slot 2 after the step-down: %d of %d Query Groups completed", got, queryGroups)
	}
	after, never2 := fixture.cacheHeld(former), fixture.cacheHeld(never)
	if after.compileClears != 1 || after.objectClears != 1 {
		t.Fatalf("former Leader's clears = compile %v, objects %v; want one each", after.compileClears, after.objectClears)
	}
	if never2.compileClears != 0 || never2.objectClears != 0 {
		t.Fatalf("never-led Worker's clears = compile %v, objects %v; want none", never2.compileClears, never2.objectClears)
	}
	if after.compiled != never2.compiled || after.objects != never2.objects {
		t.Fatalf("after the step-down the former Leader's caches hold %+v, the never-led Worker's %+v; want the same", after, never2)
	}
}
