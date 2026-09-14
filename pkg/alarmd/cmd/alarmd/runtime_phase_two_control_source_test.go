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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// controlSourceFixture is a bundle whose control runtime and clock the test
// drives by hand: Start applies the initial control result, and each
// refreshAndReconcile(ctx, true) applies the next.
type controlSourceFixture struct {
	t       *testing.T
	ctx     context.Context
	bundle  *phaseTwoWorkerBundle
	control *fakePhaseTwoControl
	owner   *fakePhaseTwoOwnership
	clock   time.Time
}

func newControlSourceFixture(t *testing.T, control *fakePhaseTwoControl, owner *fakePhaseTwoOwnership) *controlSourceFixture {
	t.Helper()
	cfg := validGoAccessRuntimeConfig()
	fixture := &controlSourceFixture{t: t, ctx: context.Background(), control: control, owner: owner, clock: time.Unix(1_700_000_000, 0)}
	bundle, err := newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
		Config: cfg, Health: newPhaseTwoApplicationHealth(), Control: control, Ownership: owner,
		Observer: observability.NopObserver{}, Now: func() time.Time { return fixture.clock },
	})
	if err != nil {
		t.Fatalf("newPhaseTwoWorkerBundle() error = %v", err)
	}
	fixture.bundle = bundle
	return fixture
}

func (fixture *controlSourceFixture) start() {
	fixture.t.Helper()
	if err := fixture.bundle.Start(fixture.ctx); err != nil {
		fixture.t.Fatalf("Start() error = %v", err)
	}
	fixture.t.Cleanup(func() { _ = fixture.bundle.Shutdown(context.Background()) })
}

func (fixture *controlSourceFixture) tick() {
	fixture.t.Helper()
	if err := fixture.bundle.refreshAndReconcile(fixture.ctx, true); err != nil {
		fixture.t.Fatalf("refreshAndReconcile() error = %v", err)
	}
}

func degradedBy(cause error) phaseTwoControlRefreshResult {
	return phaseTwoControlRefreshResult{
		QueryGroups: []execution.QueryGroupIdentity{"query-group-1"},
		Status:      phaseTwoControlDegradedLastGood, SourceKind: observability.SourceKindLegacyStrategy,
		ReasonCode: observability.ReasonContractRetryable, Cause: cause,
	}
}

func healthyResult() phaseTwoControlRefreshResult {
	return phaseTwoControlRefreshResult{QueryGroups: []execution.QueryGroupIdentity{"query-group-1"}, Status: phaseTwoControlHealthy}
}

// A leader whose every round fails is a state, not a transition. Before
// this the bundle reported the transition once and the state was invisible:
// the mode gauge read nothing, the fleet snapshot carried nothing, and a
// deployment whose source had been broken for three releases was HEALTHY.
// Now: the mode is never_succeeded while no success is known anywhere, the
// last failure's exit and text travel on the snapshot, the process becomes
// stale past the bound on its own clock, and a round that succeeds turns
// all of it back.
func TestControlSourceStateOnALeaderWhoseRoundsFail(t *testing.T) {
	cause := &controlplane.SourceRefreshFailure{Exit: controlplane.SourceRefreshExitActiveSetInvalidID,
		Err: errors.New(`alarmd controlplane: legacy Redis source incomplete: active strategy identity is not a canonical positive integer: element 3 of 979 is "\"x\""`)}
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"},
		initialResult: degradedBy(cause), refreshResults: []phaseTwoControlRefreshResult{degradedBy(cause), degradedBy(cause), healthyResult()}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: newFakePhaseTwoQueryGroup()}
	fixture := newControlSourceFixture(t, control, owner)
	if stats := fixture.bundle.controlSourceStats(); stats.Known {
		t.Fatalf("stats before Start = %+v, want unknown", stats)
	}
	if facts := fixture.bundle.controlSourceFleetFacts(); facts != nil {
		t.Fatalf("fleet facts before Start = %+v, want none", facts)
	}
	fixture.start()

	stats := fixture.bundle.controlSourceStats()
	if !stats.Known || stats.Role != observability.ControlSourceRoleLeader || stats.Mode != observability.ControlSourceModeNeverSucceeded ||
		!stats.LastSuccessAt.IsZero() {
		t.Fatalf("stats after a failed initial round = %+v, want leader/never_succeeded with no success known", stats)
	}
	facts := fixture.bundle.controlSourceFleetFacts()
	if facts == nil || facts.Role != "leader" || facts.Mode != "never_succeeded" || facts.StaleBeyondBound ||
		facts.LastFailureExit != "active_set_invalid_id" || !strings.Contains(facts.LastFailure, `element 3 of 979`) ||
		facts.LastSuccessAgeSeconds != nil || facts.DegradedSecondsThisProcess == nil || *facts.DegradedSecondsThisProcess != 0 {
		t.Fatalf("fleet facts after a failed initial round = %+v", facts)
	}
	// Two more failed rounds inside the bound: still not stale, and the
	// failure facts are those of the latest round, not of the transition.
	fixture.clock = fixture.clock.Add(time.Minute)
	fixture.tick()
	fixture.clock = fixture.clock.Add(time.Minute)
	fixture.tick()
	if facts := fixture.bundle.controlSourceFleetFacts(); facts.StaleBeyondBound || *facts.DegradedSecondsThisProcess != 120 {
		t.Fatalf("fleet facts two minutes in = %+v, want not stale and 120 s degraded", facts)
	}
	// Past the bound with no success ever known: stale on this process's own
	// clock, which is the only clock there is for a source broken from the
	// first minute.
	fixture.clock = fixture.clock.Add(controlplane.SourceStalenessBound)
	if facts := fixture.bundle.controlSourceFleetFacts(); !facts.StaleBeyondBound {
		t.Fatalf("fleet facts past the bound = %+v, want stale", facts)
	}
	// The other direction: a round succeeds.
	fixture.tick()
	stats = fixture.bundle.controlSourceStats()
	facts = fixture.bundle.controlSourceFleetFacts()
	if stats.Mode != observability.ControlSourceModeHealthy || facts.Mode != "healthy" || facts.StaleBeyondBound ||
		facts.LastFailureExit != "" || facts.LastFailure != "" || facts.DegradedSecondsThisProcess != nil {
		t.Fatalf("after a successful round: stats %+v facts %+v, want healthy with the failure cleared", stats, facts)
	}
}

// The age of the last success is the persisted fact, not this process's
// memory: a process that starts against a source that has been failing for
// an hour reads an hour on its first snapshot, and is stale at once. A
// release cannot wash that reading back to zero, which is what the
// process-local reading did on every release.
func TestControlSourceAgeComesFromThePersistedSuccess(t *testing.T) {
	cause := &controlplane.SourceRefreshFailure{Exit: controlplane.SourceRefreshExitActiveSetDuplicate, Err: errors.New("duplicate")}
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"},
		initialResult: degradedBy(cause), refreshResults: []phaseTwoControlRefreshResult{degradedBy(cause)}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: newFakePhaseTwoQueryGroup()}
	fixture := newControlSourceFixture(t, control, owner)
	control.successAt = fixture.clock.Add(-time.Hour)
	fixture.start()

	stats := fixture.bundle.controlSourceStats()
	facts := fixture.bundle.controlSourceFleetFacts()
	if stats.Mode != observability.ControlSourceModeDegradedLastGood || !stats.LastSuccessAt.Equal(control.successAt) ||
		facts.Mode != "degraded_last_good" || !facts.StaleBeyondBound || facts.LastSuccessAgeSeconds == nil ||
		*facts.LastSuccessAgeSeconds != 3600 || *facts.DegradedSecondsThisProcess != 0 {
		t.Fatalf("a young process against an old failure: stats %+v facts %+v, want degraded_last_good, age 3600, stale", stats, facts)
	}
	// A read of the persisted mark that fails leaves the copy standing: the
	// age keeps rising rather than vanishing.
	control.successErr = errors.New("store down")
	fixture.clock = fixture.clock.Add(time.Minute)
	fixture.tick()
	if facts := fixture.bundle.controlSourceFleetFacts(); facts.LastSuccessAgeSeconds == nil || *facts.LastSuccessAgeSeconds != 3660 {
		t.Fatalf("age after a failed mark read = %+v, want 3660 from the standing copy", facts)
	}
}

// A follower reports its role as follower, reads the same persisted age as
// the leader, and recovers when the activation it reads becomes readable
// again. Before this a follower that had once found the activation
// unreadable stayed degraded until it became leader and refreshed -- on a
// deployment whose leader never changes, forever.
func TestControlSourceStateOnAFollower(t *testing.T) {
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: newFakePhaseTwoQueryGroup(), follower: true}
	fixture := newControlSourceFixture(t, control, owner)
	control.successAt = fixture.clock.Add(-20 * time.Second)
	fixture.start()

	stats := fixture.bundle.controlSourceStats()
	if !stats.Known || stats.Role != observability.ControlSourceRoleFollower || stats.Mode != observability.ControlSourceModeHealthy ||
		!stats.LastSuccessAt.Equal(control.successAt) {
		t.Fatalf("follower stats = %+v, want follower/healthy with the persisted success", stats)
	}
	if facts := fixture.bundle.controlSourceFleetFacts(); facts.Role != "follower" || facts.StaleBeyondBound || facts.LeaderAbsentBeyondBound ||
		*facts.LastSuccessAgeSeconds != 20 {
		t.Fatalf("follower facts = %+v", facts)
	}
	// The leader stops succeeding; the persisted mark stops moving; the
	// follower reads it as stale past the bound, from the same fact.
	fixture.clock = fixture.clock.Add(controlplane.SourceStalenessBound + time.Minute)
	fixture.tick()
	if facts := fixture.bundle.controlSourceFleetFacts(); !facts.StaleBeyondBound || facts.Mode != "healthy" {
		t.Fatalf("follower facts with a stale persisted mark = %+v, want stale while its own reads stay healthy", facts)
	}
	// The activation becomes unreadable: the follower is degraded on its
	// own reads. Readable again: it recovers on the next tick, which it did
	// not before.
	control.loadActiveErr = controlplane.ErrSnapshotUnavailable
	fixture.tick()
	if facts := fixture.bundle.controlSourceFleetFacts(); facts.Mode != "degraded_last_good" {
		t.Fatalf("follower facts with the activation unreadable = %+v, want degraded_last_good", facts)
	}
	if health := fixture.bundle.dependencies.Health.HealthSnapshot(); health.State != observability.HealthDegraded {
		t.Fatalf("readiness with the activation unreadable = %+v, want degraded", health)
	}
	control.loadActiveErr = nil
	fixture.tick()
	if facts := fixture.bundle.controlSourceFleetFacts(); facts.Mode != "healthy" {
		t.Fatalf("follower facts after the activation is readable again = %+v, want healthy", facts)
	}
	if health := fixture.bundle.dependencies.Health.HealthSnapshot(); health.State != observability.HealthReady {
		t.Fatalf("readiness after the activation is readable again = %+v, want ready", health)
	}
}

// A process that cannot acquire the lease and cannot learn who holds it is
// unacquired, which is not follower: nobody may be refreshing. Past the
// bound in that state the snapshot says so, on a fact no refresh counter
// can show.
func TestControlSourceStateWhenTheLeaseCannotBeAcquired(t *testing.T) {
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: newFakePhaseTwoQueryGroup(), follower: true}
	fixture := newControlSourceFixture(t, control, owner)
	fixture.start()
	owner.injectFailure("acquire", errors.New("store down"), -1)
	if err := fixture.bundle.refreshAndReconcile(fixture.ctx, true); err != nil {
		t.Fatalf("a scoped acquisition failure stopped the tick: %v", err)
	}
	facts := fixture.bundle.controlSourceFleetFacts()
	if facts.Role != "unacquired" || facts.LeaderAbsentBeyondBound {
		t.Fatalf("facts right after the acquisition fails = %+v, want unacquired inside the bound", facts)
	}
	fixture.clock = fixture.clock.Add(controlplane.SourceStalenessBound + time.Second)
	if facts := fixture.bundle.controlSourceFleetFacts(); !facts.LeaderAbsentBeyondBound {
		t.Fatalf("facts past the bound unacquired = %+v, want leader absent", facts)
	}
	// The store answers again and the lease is held elsewhere: follower, and
	// the absence clears.
	owner.injectFailure("acquire", nil, 0)
	if err := fixture.bundle.refreshAndReconcile(fixture.ctx, true); err != nil {
		t.Fatal(err)
	}
	if facts := fixture.bundle.controlSourceFleetFacts(); facts.Role != "follower" || facts.LeaderAbsentBeyondBound {
		t.Fatalf("facts after the store answers = %+v, want follower with no absence", facts)
	}
}
