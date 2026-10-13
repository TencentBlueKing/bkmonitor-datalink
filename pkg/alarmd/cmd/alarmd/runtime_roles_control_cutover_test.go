// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

// stopLikeAPod stops a process the way SIGTERM does: the run context goes
// first, which ends the role runtime and its record, then the bundle drains
// and releases what it holds.
func (fixture *electionFixture) stopLikeAPod(process *electionProcess) {
	fixture.t.Helper()
	_ = process.runtime.Close()
	if err := process.bundle.Shutdown(context.Background()); err != nil {
		fixture.t.Logf("%s Shutdown: %v", process.name, err)
	}
	process.alive = false
}

// TestAWholeDeploymentRollingAtOnceElectsTheControlProcessInOneEpoch is the
// move to the split form as a release makes it: a long-running all-roles
// process leads and its record has said ready for longer than a window; a
// control process and two Workers start together, all of them young; the
// old process then stops as a Pod does. The control process leads from the
// next round, under the very next epoch, with no Worker term before it and
// no handback after; the Workers, deciding first in every round, never take
// the lease.
func TestAWholeDeploymentRollingAtOnceElectsTheControlProcessInOneEpoch(t *testing.T) {
	const queryGroups = 4
	fixture := newElectionFixture(t, queryGroups, "alarmd:phase-two:election-cutover")
	round := func() {
		fixture.advance(5 * time.Second)
		time.Sleep(1200 * time.Millisecond)
		fixture.tick()
	}
	old := fixture.open("cutover-old", roles.Set{roles.Control, roles.Worker}, true)
	if leader, _ := fixture.leader(); leader != old {
		t.Fatalf("leader before the rollout = %v, want the all-roles process", leader)
	}
	window := controlCandidacyWindow(old.bundle.dependencies.Config)
	for elapsed := time.Duration(0); elapsed <= window; elapsed += 5 * time.Second {
		round()
	}
	_, oldEpoch := fixture.leader()

	// The new Pods. The Workers are opened first, so every round lets them
	// decide before the control process does.
	workers := []*electionProcess{
		fixture.open("cutover-worker-1", roles.Set{roles.Worker}, true),
		fixture.open("cutover-worker-2", roles.Set{roles.Worker}, true),
	}
	control := fixture.open("cutover-control", roles.Set{roles.Control}, true)
	round()
	if leader, epoch := fixture.leader(); leader != old || epoch != oldEpoch {
		t.Fatalf("leader while the old process still runs = %v epoch %d, want %s epoch %d", leader, epoch, old.name, oldEpoch)
	}

	fixture.stopLikeAPod(old)
	if leader, _ := fixture.leader(); leader != nil {
		t.Fatalf("%s leads the moment the old Leader stopped", leader.name)
	}
	// What the fallbacks read: the old process's record no longer says
	// ready, so it holds nobody off.
	registry, err := ownership.NewRedisStoreWithClient(fixture.redis, productionPhaseTwoPrefix(fixture.prefix, "ownership"))
	if err != nil {
		t.Fatal(err)
	}
	if record, found, err := registry.ReadInstance(fixture.ctx, old.name); err != nil || !found || record.ControlCandidacyReady {
		t.Fatalf("old process's record after it stopped = (%+v, %v, %v), want present and not ready", record, found, err)
	}

	round()
	leader, epoch := fixture.leader()
	if leader != control || epoch != oldEpoch+1 {
		t.Fatalf("leader one round after the old Leader stopped = %v epoch %d, want %s epoch %d", leader, epoch, control.name, oldEpoch+1)
	}
	for elapsed := time.Duration(0); elapsed < 2*window; elapsed += 5 * time.Second {
		round()
		if now, nowEpoch := fixture.leader(); now != control || nowEpoch != epoch {
			t.Fatalf("leader %s after the cut-over = %v epoch %d, want %s epoch %d", elapsed, now, nowEpoch, control.name, epoch)
		}
	}
	if takeovers, handbacks := fixture.transitions("fallback", "fallback_takeover"), fixture.transitions("preferred", "handback"); takeovers != 0 || handbacks != 0 {
		t.Fatalf("fallback takeovers %v and handbacks %v over the cut-over, want none", takeovers, handbacks)
	}
	for _, worker := range workers {
		if got := counterValue(t, worker.recorder, "bkmonitor_alarmd_control_leader_transitions_total",
			map[string]string{"to_class": "fallback", "reason": "fallback_takeover"}); got != 0 {
			t.Fatalf("%s took the lease %v times", worker.name, got)
		}
	}
}

// TestYoungWorkersWaitForAReadyControlProcessNoLongerThanAWindow is the
// bound on the wait the cut-over relies on. A control process keeps
// restarting, so its record says ready but never for a window, and it never
// takes the lease. Workers that have just started hold off while it says
// ready, until they have been ready for a window themselves; then one takes
// the free lease, so a control process in a crash loop still cannot leave a
// deployment without a Leader.
func TestYoungWorkersWaitForAReadyControlProcessNoLongerThanAWindow(t *testing.T) {
	fixture := newElectionFixture(t, 2, "alarmd:phase-two:election-young-fallback")
	fixture.controlLeaderTTL = time.Hour
	flapping, flappingHealth := fixture.flappingPreferred("young-control")
	write := func(ready bool) {
		flappingHealth.candidacyReady.Store(ready)
		flapping.writeInstance(fixture.ctx, flappingHealth, "disabled", false)
	}
	write(true)
	started := fixture.now()
	fixture.open("young-worker-1", roles.Set{roles.Worker}, true)
	fixture.open("young-worker-2", roles.Set{roles.Worker}, true)
	if leader, _ := fixture.leader(); leader != nil {
		t.Fatalf("%s took the lease on starting while a control process said ready", leader.name)
	}
	window := controlCandidacyWindow(flapping.config)
	for offset := 5 * time.Second; ; offset += 5 * time.Second {
		fixture.clockMillis.Store(started.Add(offset).UnixMilli())
		if offset%(30*time.Second) == 0 {
			// A restart: one write not ready, the next incarnation ready.
			write(false)
		}
		write(true)
		fixture.tick()
		leader, _ := fixture.leader()
		if offset < window && leader != nil {
			t.Fatalf("%s took the lease %s after starting, while a control process said ready", leader.name, offset)
		}
		if leader != nil {
			break
		}
		if offset > window+5*time.Second {
			t.Fatalf("no Worker leads %s after starting, a window and a round past the start", offset)
		}
	}
	if got := fixture.transitions("fallback", "fallback_takeover"); got != 1 {
		t.Fatalf("fallback takeovers = %v, want 1", got)
	}
}
