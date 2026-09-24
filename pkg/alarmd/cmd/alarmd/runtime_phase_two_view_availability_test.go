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
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// orderLog records what the leader did, in order, across the fakes.
type orderLog struct {
	mu     sync.Mutex
	events []string
}

func (log *orderLog) add(event string) {
	log.mu.Lock()
	log.events = append(log.events, event)
	log.mu.Unlock()
}

func (log *orderLog) snapshot() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]string(nil), log.events...)
}

func (log *orderLog) count(event string) int {
	n := 0
	for _, e := range log.snapshot() {
		if e == event {
			n++
		}
	}
	return n
}

type viewAvailabilityControl struct {
	*fakePhaseTwoControl
	log *orderLog
}

func (control *viewAvailabilityControl) Refresh(ctx context.Context) (phaseTwoControlRefreshResult, error) {
	control.log.add("refresh")
	return control.fakePhaseTwoControl.Refresh(ctx)
}

type viewAvailabilityOwnership struct {
	*fakePhaseTwoOwnership
	log *orderLog
	// hold, when set, keeps MaintainControlLeader running past its context:
	// a leader task that does not stop in time.
	hold chan struct{}
}

func (owner *viewAvailabilityOwnership) PublishStoredView(context.Context) {
	owner.log.add("stored_view")
}

func (owner *viewAvailabilityOwnership) ReleaseControlLeader(context.Context) error {
	owner.log.add("release_leader")
	return nil
}

func (owner *viewAvailabilityOwnership) MaintainControlLeader(ctx context.Context, interval, ttl time.Duration) error {
	if owner.hold != nil {
		<-owner.hold
		return nil
	}
	return owner.fakePhaseTwoOwnership.MaintainControlLeader(ctx, interval, ttl)
}

// A new term publishes the stored view before its first refresh, once; the
// rounds after it do not repeat it. Before, a Worker without a view waited
// for the whole refresh.
func TestANewTermPublishesTheStoredViewBeforeItsFirstRefreshOnce(t *testing.T) {
	log := &orderLog{}
	control := &viewAvailabilityControl{fakePhaseTwoControl: &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}, log: log}
	runner := newFakePhaseTwoQueryGroup()
	owner := &viewAvailabilityOwnership{fakePhaseTwoOwnership: &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"},
		runner: runner}, log: log}
	cfg := validGoAccessRuntimeConfig()
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(5 * time.Millisecond)
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bundle.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for log.count("refresh") < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	events := log.snapshot()
	first := -1
	for index, event := range events {
		if event == "refresh" {
			first = index
			break
		}
	}
	stored := -1
	for index, event := range events {
		if event == "stored_view" {
			stored = index
			break
		}
	}
	if log.count("refresh") < 3 || log.count("stored_view") != 1 || stored < 0 || first < 0 || stored > first {
		t.Fatalf("events = %v, want the stored view once, before the first refresh, and no more in the term", events)
	}
}

// Shutdown gives up the Control Leader lease once every leader task has
// stopped, so the next leader does not wait for the lease to expire.
func TestShutdownReleasesTheLeaderLeaseAfterItsTasksStop(t *testing.T) {
	log := &orderLog{}
	control := &viewAvailabilityControl{fakePhaseTwoControl: &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}, log: log}
	runner := newFakePhaseTwoQueryGroup()
	owner := &viewAvailabilityOwnership{fakePhaseTwoOwnership: &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: runner}, log: log}
	bundle := mustPhaseTwoWorkerBundle(t, validGoAccessRuntimeConfig(), newPhaseTwoApplicationHealth(), control, owner)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bundle.Run(ctx) }()
	waitSignal(t, runner.leaseStarted, "query-group lease maintenance")
	cancel()
	<-done
	if log.count("release_leader") != 1 {
		t.Fatalf("events = %v, want the leader lease released once", log.snapshot())
	}
}

// A leader task still running when the shutdown wait gives up keeps the
// lease: releasing it would let the next leader start while this one still
// writes. It is left to expire, as it always was.
func TestALeaderTaskThatDoesNotStopKeepsTheLease(t *testing.T) {
	log := &orderLog{}
	control := &viewAvailabilityControl{fakePhaseTwoControl: &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}, log: log}
	runner := newFakePhaseTwoQueryGroup()
	hold := make(chan struct{})
	defer close(hold)
	owner := &viewAvailabilityOwnership{fakePhaseTwoOwnership: &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: runner},
		log: log, hold: hold}
	cfg := validGoAccessRuntimeConfig()
	cfg.ShutdownTimeout = config.Duration(50 * time.Millisecond)
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bundle.Run(ctx) }()
	waitSignal(t, runner.leaseStarted, "query-group lease maintenance")
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not return")
	}
	if log.count("release_leader") != 0 {
		t.Fatalf("events = %v, want the lease kept while a leader task still runs", log.snapshot())
	}
}

// storedViewStore is the ownership store as the stored view reads it: the
// Assignment records of the Query Groups asked for.
type storedViewStore struct {
	productionPhaseTwoOwnershipStore
	records map[execution.QueryGroupIdentity]ownership.AssignmentRecord
	asked   []execution.QueryGroupIdentity
}

func (store *storedViewStore) ReadAssignments(_ context.Context, queryGroups []execution.QueryGroupIdentity) (
	map[execution.QueryGroupIdentity]ownership.AssignmentRecord, ownership.ControlReadStats, error) {
	store.asked = append(store.asked, queryGroups...)
	result := make(map[execution.QueryGroupIdentity]ownership.AssignmentRecord)
	for _, queryGroup := range queryGroups {
		if record, ok := store.records[queryGroup]; ok {
			result[queryGroup] = record
		}
	}
	return result, ownership.ControlReadStats{}, nil
}

type storedViewSource struct {
	staticViewSource
	active    []execution.QueryGroupIdentity
	activeErr error
	draining  []controlplane.DrainingQueryGroup
}

func (source storedViewSource) LoadActivationHead(ctx context.Context) (controlplane.ActivationState, error) {
	state, err := source.staticViewSource.LoadActivationHead(ctx)
	state.Draining = source.draining
	return state, err
}

func (source storedViewSource) LoadActiveQueryGroupSet(context.Context, controlplane.ActiveQueryGroupSetRef) ([]execution.QueryGroupIdentity, error) {
	return source.active, source.activeErr
}

func storedViewRuntime(t *testing.T, source storedViewSource, store *storedViewStore, observed *[]observability.Observation) (*productionPhaseTwoOwnership, *viewstream.Server, ownership.PublicationAuthority) {
	t.Helper()
	server, err := viewstream.NewServer(viewStreamAdmission{registry: nil, now: time.Now}, nil, viewstream.ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	authority := ownership.PublicationAuthority{Fence: execution.OwnerFence{
		QueryGroup: ownership.ControlLeaderIdentity, OwnerID: "leader", OwnerEpoch: 7, LeaseToken: "token",
	}, Deadline: time.Now().Add(time.Minute)}
	if err := server.Lead(authority.Fence.OwnerEpoch); err != nil {
		t.Fatal(err)
	}
	runtime := &productionPhaseTwoOwnership{authority: authority, dependencies: productionPhaseTwoOwnershipDependencies{
		ViewStream: server, ViewSource: source, Store: store,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			*observed = append(*observed, observation)
		}),
	}}
	return runtime, server, authority
}

// The stored view is published from the activation's active set and Draining
// Query Groups and their records, under the term's epoch; the assignment
// round that follows in the same term publishes again under the same epoch
// and supersedes it.
func TestTheStoredViewIsTheActivationsSetAndTheRoundSupersedesIt(t *testing.T) {
	var observed []observability.Observation
	records := map[execution.QueryGroupIdentity]ownership.AssignmentRecord{
		"qg-active":   {DesiredWorkerID: "worker-a", RecordRevision: 3},
		"qg-draining": {DesiredWorkerID: "worker-b", RecordRevision: 2},
	}
	store := &storedViewStore{records: records}
	source := storedViewSource{active: []execution.QueryGroupIdentity{"qg-active"},
		draining: []controlplane.DrainingQueryGroup{{QueryGroup: "qg-draining", RetiredBoundary: 60}}}
	runtime, server, authority := storedViewRuntime(t, source, store, &observed)
	runtime.PublishStoredView(context.Background())
	stored := server.Stats()
	if stored.Revision == 0 || stored.ControlEpoch != authority.Fence.OwnerEpoch || len(observed) != 0 {
		t.Fatalf("after the stored view: stats %+v observed %+v", stored, observed)
	}
	if len(store.asked) != 2 {
		t.Fatalf("records asked for %v, want the active and the draining Query Group", store.asked)
	}
	changed := map[execution.QueryGroupIdentity]ownership.AssignmentRecord{"qg-active": {DesiredWorkerID: "worker-c", RecordRevision: 4}}
	runtime.publishView(context.Background(), authority, changed, nil)
	round := server.Stats()
	if round.ControlEpoch != stored.ControlEpoch || round.Revision <= stored.Revision {
		t.Fatalf("the round's view (%+v) does not supersede the stored one (%+v)", round, stored)
	}
}

// Nothing is published from an active set that cannot be read or is empty:
// an empty view under a newer epoch would take every Query Group off every
// Worker that already holds one.
func TestNoStoredViewIsPublishedFromAnUnreadableOrEmptySet(t *testing.T) {
	for name, source := range map[string]storedViewSource{
		"unreadable": {activeErr: errors.New("redis gone")},
		"empty":      {},
		// Active Query Groups none of which has a record yet: a fresh
		// deployment before its first assignment round.
		"no records": {active: []execution.QueryGroupIdentity{"qg-new"}},
	} {
		t.Run(name, func(t *testing.T) {
			var observed []observability.Observation
			store := &storedViewStore{}
			runtime, server, _ := storedViewRuntime(t, source, store, &observed)
			runtime.PublishStoredView(context.Background())
			if stats := server.Stats(); stats.Revision != 0 {
				t.Fatalf("a view was published: %+v", stats)
			}
			if name == "unreadable" && (len(observed) != 1 || observed[0].ViewStream == nil || observed[0].ViewStream.Event != "publish_failed") {
				t.Fatalf("observed %+v, want the unreadable set named", observed)
			}
		})
	}
}

var _ scheduler.AssignmentStore = (*storedViewStore)(nil)

// Against the store itself: a released Control Leader lease is taken by the
// next replica at once rather than when it would have expired, and releasing
// a lease this process does not hold is nothing to report.
func TestAReleasedLeaderLeaseIsTakenAtOnce(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	store, err := ownership.NewRedisStoreWithClient(client, "alarmd:test:leader-release")
	if err != nil {
		t.Fatal(err)
	}
	runtime := func(worker string) *productionPhaseTwoOwnership {
		return &productionPhaseTwoOwnership{dependencies: productionPhaseTwoOwnershipDependencies{
			Store: store, WorkerID: worker, ControlLeaderTTL: time.Minute, Now: time.Now,
		}}
	}
	first, second := runtime("worker-a"), runtime("worker-b")
	ctx := context.Background()
	if err := second.ReleaseControlLeader(ctx); err != nil {
		t.Fatalf("releasing a lease never held: %v", err)
	}
	if leader, err := first.TryAcquireControlLeader(ctx, time.Now(), time.Minute); err != nil || !leader {
		t.Fatalf("first acquire: %v %v", leader, err)
	}
	if leader, err := second.TryAcquireControlLeader(ctx, time.Now(), time.Minute); err != nil || leader {
		t.Fatalf("second acquire while held: %v %v", leader, err)
	}
	if err := first.ReleaseControlLeader(ctx); err != nil {
		t.Fatal(err)
	}
	if leader, err := second.TryAcquireControlLeader(ctx, time.Now(), time.Minute); err != nil || !leader {
		t.Fatalf("second acquire after the release: %v %v, want it at once", leader, err)
	}
}

// The bound is a constant, tested on both sides.
func TestTheViewStallBoundIsReadOnBothSides(t *testing.T) {
	at := time.Unix(10_000, 0)
	inside := viewStreamFacts(viewstream.Stats{Leading: true, PublishFailingSince: at.Add(-fleet.ViewStreamStallBound + time.Second),
		NoSessionsSince: at.Add(-fleet.ViewStreamStallBound + time.Second)}, at)
	beyond := viewStreamFacts(viewstream.Stats{Leading: true, PublishFailingSince: at.Add(-fleet.ViewStreamStallBound - time.Second),
		NoSessionsSince: at.Add(-fleet.ViewStreamStallBound - time.Second)}, at)
	if inside.PublishFailingBeyondBound || inside.NoSessionsBeyondBound || inside.PublishFailingSeconds == nil {
		t.Fatalf("inside the bound: %+v", inside)
	}
	if !beyond.PublishFailingBeyondBound || !beyond.NoSessionsBeyondBound {
		t.Fatalf("beyond the bound: %+v", beyond)
	}
	if healthy := viewStreamFacts(viewstream.Stats{Leading: true}, at); healthy.PublishFailingSeconds != nil || healthy.NoSessionsSeconds != nil {
		t.Fatalf("a healthy Leader carries ages: %+v", healthy)
	}
}

// blockedViewSource publishes two Query Groups, one of which a cutover held
// back with its open Segment naming older content.
type blockedViewSource struct {
	staticViewSource
}

func (blockedViewSource) LoadPublishedContent(context.Context, controlplane.SnapshotPublicationRef) (controlplane.PublishedContent, error) {
	return controlplane.PublishedContent{Groups: map[execution.QueryGroupIdentity]controlplane.ContentEntry{
		"qg-a": {Digest: "da"}, "qg-b": {Digest: "db-new"},
	}}, nil
}

func (blockedViewSource) ActivationBlocked(context.Context) ([]controlplane.BlockedQueryGroup, error) {
	return []controlplane.BlockedQueryGroup{{QueryGroup: "qg-b", Reason: controlplane.CutoverReasonOpenDigestMismatch, OpenDigest: "db-open"}}, nil
}

// The view gives a held-back Query Group the content its open Segment names,
// which is what the Worker runs and what its scope says; the manifest's new
// content would stop it on a scope mismatch.
func TestTheViewGivesAHeldBackQueryGroupItsOpenSegment(t *testing.T) {
	var observed []observability.Observation
	records := map[execution.QueryGroupIdentity]ownership.AssignmentRecord{
		"qg-a": {DesiredWorkerID: "worker-a", RecordRevision: 3},
		"qg-b": {DesiredWorkerID: "worker-a", RecordRevision: 2},
	}
	runtime, server, authority := storedViewRuntime(t, storedViewSource{}, &storedViewStore{records: records}, &observed)
	runtime.dependencies.ViewSource = blockedViewSource{}
	runtime.publishView(context.Background(), authority, records, nil)
	view, ok := server.Snapshot("worker-a")
	if !ok || len(observed) != 0 {
		t.Fatalf("nothing published: ok=%v observed=%+v", ok, observed)
	}
	content := map[execution.QueryGroupIdentity]string{}
	for _, entry := range view.Entries {
		if entry.Content != nil {
			content[entry.QueryGroup] = string(entry.Content.ObjectDigest)
		}
	}
	if content["qg-a"] != "da" || content["qg-b"] != "db-open" {
		t.Fatalf("view content = %v, want qg-a from the manifest and qg-b from its open Segment", content)
	}
}
