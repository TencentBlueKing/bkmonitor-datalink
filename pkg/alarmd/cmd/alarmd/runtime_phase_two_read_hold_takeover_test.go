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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
)

// The read-hold record is read back when the Assignment first names this
// Worker (AssignedQueryGroups), which can be a lease TTL before the lease is
// acquired; the old owner may write it in between. The new owner must start
// from the record as it stands at its takeover: the hold is read back at
// takeover, before replay is classified.
func TestANewOwnerReadsTheReadHoldRecordAsItStandsAtItsTakeover(t *testing.T) {
	ctx := context.Background()
	fixture := newRenewalTestFixture(t)
	holds, store, _ := runtimeTestHolds(t)
	holds.now = fixture.clock.Now
	fixture.production.dependencies.ReadHolds = holds
	qg := execution.QueryGroupIdentity("query-group-1")
	before, _ := json.Marshal(readhold.Record{SinceSlot: 1, HoldMillis: 60000})
	store.values[qg] = before
	// The reconcile that first sees the Assignment name this Worker.
	holds.restore(ctx, []execution.QueryGroupIdentity{qg})
	// The old owner, its lease still live, raises the hold.
	after, _ := json.Marshal(readhold.Record{SinceSlot: 1, HoldMillis: 120000})
	store.values[qg] = after
	// The lease frees; this Worker acquires and the next reconcile restores.
	if _, err := fixture.production.OpenQueryGroup(ctx, qg, fixture.clock.Now(), time.Minute); err != nil {
		t.Fatal(err)
	}
	holds.restore(ctx, []execution.QueryGroupIdentity{qg})
	if got := holds.ReadHold(qg); got != 120*time.Second {
		t.Fatalf("new owner holds h=%s from before its takeover, record says 120s", got)
	}
}

// The read the Assignment read ends with reads only the groups this Worker
// holds a binding for. A group not opened here yet is read by the round that
// opens it, after its lease is held - never while another owner's lease may
// still be live.
func TestTheRestoreBeforeALeaseReadsNoGroupThisWorkerHasNotOpened(t *testing.T) {
	ctx := context.Background()
	fixture := newRenewalTestFixture(t)
	holds, store, _ := runtimeTestHolds(t)
	holds.now = fixture.clock.Now
	fixture.production.dependencies.ReadHolds = holds
	opened, unopened := execution.QueryGroupIdentity("query-group-1"), execution.QueryGroupIdentity("query-group-2")
	if _, err := fixture.production.OpenQueryGroup(ctx, opened, fixture.clock.Now(), time.Minute); err != nil {
		t.Fatal(err)
	}
	holds.restore(ctx, []execution.QueryGroupIdentity{opened, unopened})
	if want := [][]execution.QueryGroupIdentity{{opened}}; !reflect.DeepEqual(store.reads, want) {
		t.Fatalf("the restore read %v, want only the opened group %v", store.reads, want)
	}
	if holds.controller.Inspect(unopened).Loaded {
		t.Fatal("a group this Worker has not opened was loaded before its lease")
	}
}

// A hold already loaded when the lease is taken - read as another group's
// predecessor while a different Worker owned this one - is read again by the
// round that opens the group: that round reads every group it opened,
// loaded or not, and the owner starts from the record as it stands then.
func TestTheRoundThatOpensAGroupRereadsAHoldLoadedBeforeItsLease(t *testing.T) {
	ctx := context.Background()
	fixture := newRenewalTestFixture(t)
	holds, store, _ := runtimeTestHolds(t)
	holds.now = fixture.clock.Now
	fixture.production.dependencies.ReadHolds = holds
	qg := execution.QueryGroupIdentity("query-group-1")
	before, _ := json.Marshal(readhold.Record{SinceSlot: 1, HoldMillis: 60000})
	store.values[qg] = before
	// Read as another group's predecessor, before this Worker held it.
	if err := holds.controller.RestoreBatch(ctx, []execution.QueryGroupIdentity{qg}); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(readhold.Record{SinceSlot: 1, HoldMillis: 120000})
	store.values[qg] = after
	if _, err := fixture.production.OpenQueryGroup(ctx, qg, fixture.clock.Now(), time.Minute); err != nil {
		t.Fatal(err)
	}
	fixture.production.LoadOpened(ctx, []execution.QueryGroupIdentity{qg})
	if got := holds.ReadHold(qg); got != 120*time.Second {
		t.Fatalf("new owner holds h=%s loaded before its lease, record says 120s", got)
	}
}

// One group's read answered with an error leaves that group unloaded and the
// rest of its round loaded; the next reconcile's restore reads it again, and
// only it.
func TestAGroupWhoseReadFailsAtTakeoverIsReadAgainByTheNextReconcile(t *testing.T) {
	ctx := context.Background()
	fixture := newRenewalTestFixture(t)
	holds, store, _ := runtimeTestHolds(t)
	holds.now = fixture.clock.Now
	fixture.production.dependencies.ReadHolds = holds
	failing, healthy := execution.QueryGroupIdentity("query-group-1"), execution.QueryGroupIdentity("query-group-2")
	for qg, millis := range map[execution.QueryGroupIdentity]int64{failing: 120000, healthy: 90000} {
		raw, _ := json.Marshal(readhold.Record{SinceSlot: 1, HoldMillis: millis})
		store.values[qg] = raw
		if _, err := fixture.production.OpenQueryGroup(ctx, qg, fixture.clock.Now(), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	store.failRead = map[execution.QueryGroupIdentity]error{failing: errors.New("answered with an error")}
	fixture.production.LoadOpened(ctx, []execution.QueryGroupIdentity{failing, healthy})
	if holds.controller.Inspect(failing).Loaded {
		t.Fatal("a group whose read failed is loaded")
	}
	if !holds.controller.Inspect(healthy).Loaded || holds.ReadHold(healthy) != 90*time.Second {
		t.Fatalf("the other group of the round: loaded %t, h=%s, want loaded with 90s",
			holds.controller.Inspect(healthy).Loaded, holds.ReadHold(healthy))
	}
	store.failRead, store.reads = nil, nil
	holds.restore(ctx, []execution.QueryGroupIdentity{failing, healthy})
	if want := [][]execution.QueryGroupIdentity{{failing}}; !reflect.DeepEqual(store.reads, want) {
		t.Fatalf("the next reconcile read %v, want the failed group alone %v", store.reads, want)
	}
	if holds.ReadHold(failing) != 120*time.Second {
		t.Fatalf("the retried group holds h=%s, record says 120s", holds.ReadHold(failing))
	}
}

// A round reads its opened groups a pipeline of ownership.ControlReadBatch at
// a time: one pipeline failing leaves its own groups unloaded and none of
// the others'.
func TestAFailedPipelineAtTakeoverLeavesOnlyItsOwnGroupsUnloaded(t *testing.T) {
	ctx := context.Background()
	fixture := newRenewalTestFixture(t)
	holds, store, _ := runtimeTestHolds(t)
	holds.now = fixture.clock.Now
	fixture.production.dependencies.ReadHolds = holds
	groups := make([]execution.QueryGroupIdentity, ownership.ControlReadBatch+1)
	for index := range groups {
		groups[index] = execution.QueryGroupIdentity(fmt.Sprintf("query-group-%03d", index))
	}
	last := groups[len(groups)-1]
	store.failBatch = map[execution.QueryGroupIdentity]error{last: errors.New("connection reset")}
	fixture.production.LoadOpened(ctx, groups)
	for _, qg := range groups[:ownership.ControlReadBatch] {
		if !holds.controller.Inspect(qg).Loaded {
			t.Fatalf("%s is unloaded: a failure in another pipeline", qg)
		}
	}
	if holds.controller.Inspect(last).Loaded {
		t.Fatal("the group whose pipeline failed is loaded")
	}
}

// A round that stops part-way - an open answered against an invariant -
// releases what it had opened. None of it is started, and a lease left held
// would keep the group from every Worker, this one included, until its TTL.
func TestARoundThatStopsPartWayReleasesWhatItOpened(t *testing.T) {
	queryGroups := []execution.QueryGroupIdentity{"query-group-1", "query-group-2"}
	first, second := newFakePhaseTwoQueryGroup(), newFakePhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{runners: map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{
		"query-group-1": first, "query-group-2": second,
	}, failOpenAt: 2, failOpenErr: newPhaseTwoInvariantError("an open answered against an invariant")}
	bundle := mustPhaseTwoWorkerBundle(t, validGoAccessRuntimeConfig(), newPhaseTwoApplicationHealth(),
		&fakePhaseTwoControl{queryGroups: queryGroups}, owner)
	if err := bundle.applyAssignment(context.Background(), queryGroups); !isPhaseTwoInvariantError(err) {
		t.Fatalf("applyAssignment() = %v, want the open's invariant error", err)
	}
	if released := first.releaseCount() + second.releaseCount(); released != 1 {
		t.Fatalf("%d releases, want the one group opened before the stop released", released)
	}
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	if len(bundle.runners) != 0 {
		t.Fatalf("a round that stopped started %v", bundle.runners)
	}
}

// A round opens every Query Group it is missing before it starts any of
// their Runners, and reads the opened groups' holds in between: once, and
// naming exactly the groups whose leases it now holds.
func TestARoundReadsItsOpenedGroupsHoldsAfterTheirLeasesAndBeforeTheirRunners(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	queryGroups := []execution.QueryGroupIdentity{"query-group-1", "query-group-2", "query-group-3"}
	control := &fakePhaseTwoControl{queryGroups: queryGroups}
	first, third := newFakePhaseTwoQueryGroup(), newFakePhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{assigned: queryGroups, runners: map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{
		"query-group-1": first, "query-group-2": newFakePhaseTwoQueryGroup(), "query-group-3": third,
	}, openErrors: map[execution.QueryGroupIdentity]error{"query-group-2": ownership.ErrLeaseBusy}}
	type load struct {
		groups         []execution.QueryGroupIdentity
		opens, running int
	}
	var (
		mu     sync.Mutex
		loads  []load
		bundle *phaseTwoWorkerBundle
	)
	owner.loadOpened = func(groups []execution.QueryGroupIdentity) {
		owner.mu.Lock()
		opens := owner.opens
		owner.mu.Unlock()
		bundle.mu.RLock()
		running := len(bundle.runners)
		bundle.mu.RUnlock()
		sorted := append([]execution.QueryGroupIdentity(nil), groups...)
		sort.Slice(sorted, func(left, right int) bool { return sorted[left] < sorted[right] })
		mu.Lock()
		loads = append(loads, load{groups: sorted, opens: opens, running: running})
		mu.Unlock()
	}
	bundle = mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = bundle.Shutdown(context.Background()) })
	waitSignal(t, first.leaseStarted, "the first group's lease maintenance")
	waitSignal(t, third.leaseStarted, "the third group's lease maintenance")
	mu.Lock()
	defer mu.Unlock()
	want := load{groups: []execution.QueryGroupIdentity{"query-group-1", "query-group-3"}, opens: 3, running: 0}
	if len(loads) == 0 || !reflect.DeepEqual(loads[0], want) {
		t.Fatalf("the round's read = %+v, want once, after all three opens and before any Runner, of the two opened %+v", loads, want)
	}
}

// A start the bundle refuses - it began draining after the round's opens -
// releases the group it opened, each of them. A release that fails once the
// round's context is canceled returns the context's error.
func TestARoundWhoseStartsAreRefusedReleasesWhatItOpened(t *testing.T) {
	for _, c := range []struct {
		name       string
		canceled   bool
		releaseErr error
		want       error
	}{
		{name: "released", want: nil},
		{name: "a release failing after cancellation", canceled: true, releaseErr: errors.New("store unreachable"), want: context.Canceled},
	} {
		t.Run(c.name, func(t *testing.T) {
			queryGroups := []execution.QueryGroupIdentity{"query-group-1", "query-group-2"}
			first, second := newFakePhaseTwoQueryGroup(), newFakePhaseTwoQueryGroup()
			first.releaseErr, second.releaseErr = c.releaseErr, c.releaseErr
			owner := &fakePhaseTwoOwnership{runners: map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{
				"query-group-1": first, "query-group-2": second,
			}}
			bundle := mustPhaseTwoWorkerBundle(t, validGoAccessRuntimeConfig(), newPhaseTwoApplicationHealth(),
				&fakePhaseTwoControl{queryGroups: queryGroups}, owner)
			// Between the opens and the starts.
			owner.loadOpened = func([]execution.QueryGroupIdentity) {
				bundle.mu.Lock()
				bundle.draining = true
				bundle.mu.Unlock()
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if c.canceled {
				cancel()
			}
			if err := bundle.applyAssignment(ctx, queryGroups); !errors.Is(err, c.want) {
				t.Fatalf("applyAssignment() = %v, want %v", err, c.want)
			}
			if first.releaseCount() != 1 || second.releaseCount() != 1 {
				t.Fatalf("releases %d and %d, want each opened group released once", first.releaseCount(), second.releaseCount())
			}
			bundle.mu.RLock()
			defer bundle.mu.RUnlock()
			if len(bundle.runners) != 0 {
				t.Fatalf("a draining bundle started %v", bundle.runners)
			}
		})
	}
}

// A round that stops part-way releases what it opened, and a release that
// fails marks the Worker degraded only while the round's context is live: a
// canceled round stops at the open that found it canceled and returns the
// context's error, its failed release a consequence of stopping, not a
// dependency failure.
func TestAStoppedRoundsFailedReleaseDegradesTheWorkerOnlyWhileItsContextIsLive(t *testing.T) {
	for _, c := range []struct {
		name     string
		canceled bool
		openErr  error
		degrades bool
	}{
		{name: "an invariant, the context live", openErr: newPhaseTwoInvariantError("an open answered against an invariant"), degrades: true},
		{name: "the context canceled", canceled: true, openErr: errors.New("connection closed")},
	} {
		t.Run(c.name, func(t *testing.T) {
			queryGroups := []execution.QueryGroupIdentity{"query-group-1", "query-group-2"}
			first, second := newFakePhaseTwoQueryGroup(), newFakePhaseTwoQueryGroup()
			first.releaseErr, second.releaseErr = errors.New("store unreachable"), errors.New("store unreachable")
			owner := &fakePhaseTwoOwnership{runners: map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{
				"query-group-1": first, "query-group-2": second,
			}, failOpenAt: 2, failOpenErr: c.openErr}
			bundle := mustPhaseTwoWorkerBundle(t, validGoAccessRuntimeConfig(), newPhaseTwoApplicationHealth(),
				&fakePhaseTwoControl{queryGroups: queryGroups}, owner)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if c.canceled {
				cancel()
			}
			before := bundle.controlDependencyFailureSeq()
			err := bundle.applyAssignment(ctx, queryGroups)
			switch {
			case c.canceled && !errors.Is(err, context.Canceled):
				t.Fatalf("applyAssignment() = %v, want the context's error", err)
			case !c.canceled && !isPhaseTwoInvariantError(err):
				t.Fatalf("applyAssignment() = %v, want the open's invariant error", err)
			}
			if released := first.releaseCount() + second.releaseCount(); released != 1 {
				t.Fatalf("%d releases, want the one group opened before the stop", released)
			}
			if degraded := bundle.controlDependencyFailureSeq() != before; degraded != c.degrades {
				t.Fatalf("the failed release marked the Worker degraded: %t, want %t", degraded, c.degrades)
			}
			bundle.mu.RLock()
			defer bundle.mu.RUnlock()
			if len(bundle.runners) != 0 {
				t.Fatalf("a round that stopped started %v", bundle.runners)
			}
		})
	}
}

// Read holds are optional on the ownership runtime, as everywhere else it
// reads them: one without them loads nothing, and the round goes on.
func TestLoadingOpenedGroupsWithoutReadHoldsReadsNothing(t *testing.T) {
	fixture := newRenewalTestFixture(t)
	fixture.production.LoadOpened(context.Background(), []execution.QueryGroupIdentity{"query-group-1"})
}
