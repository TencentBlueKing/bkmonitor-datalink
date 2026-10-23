// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// viewPublishSource is the catalog as the view publication reads it: the
// published content by Query Group under one activation, the active set
// the first view of a term reads, and the Query Groups the publication no
// longer carries - each with the content its timeline's last Segment
// names, or with the error reading that timeline gives.
type viewPublishSource struct {
	published  map[execution.QueryGroupIdentity]controlplane.ContentEntry
	active     []execution.QueryGroupIdentity
	draining   map[execution.QueryGroupIdentity]controlplane.ContentEntry
	unreadable map[execution.QueryGroupIdentity]error
}

func (source viewPublishSource) LoadActivationHead(context.Context) (controlplane.ActivationState, error) {
	state := controlplane.ActivationState{RecordRevision: 1, Current: controlplane.SnapshotPublicationRef{SnapshotRevision: "snap", PublicationEpoch: 1}}
	for queryGroup := range source.draining {
		state.Draining = append(state.Draining, controlplane.DrainingQueryGroup{QueryGroup: queryGroup})
	}
	for queryGroup := range source.unreadable {
		state.Draining = append(state.Draining, controlplane.DrainingQueryGroup{QueryGroup: queryGroup})
	}
	sort.Slice(state.Draining, func(left, right int) bool { return state.Draining[left].QueryGroup < state.Draining[right].QueryGroup })
	return state, nil
}

func (source viewPublishSource) LoadPublishedContent(context.Context, controlplane.SnapshotPublicationRef) (controlplane.PublishedContent, error) {
	return controlplane.PublishedContent{Groups: source.published}, nil
}

func (source viewPublishSource) ActivationBlocked(context.Context) ([]controlplane.BlockedQueryGroup, error) {
	return nil, nil
}

func (source viewPublishSource) LoadActiveQueryGroupSet(context.Context, controlplane.ActiveQueryGroupSetRef) ([]execution.QueryGroupIdentity, error) {
	return source.active, nil
}

func (source viewPublishSource) DrainingContent(_ context.Context, queryGroup execution.QueryGroupIdentity) (execution.ObjectDigest, []execution.OutputContextRef, bool, error) {
	if err := source.unreadable[queryGroup]; err != nil {
		return "", nil, false, err
	}
	if entry, ok := source.draining[queryGroup]; ok {
		return entry.Digest, entry.Refs, true, nil
	}
	return "", nil, false, nil
}

// viewPublishRuntime is a Leader's ownership runtime leading term 4 with a
// stream and the given catalog, recording what it observes.
func viewPublishRuntime(t *testing.T, source viewSource, observed *[]observability.Observation) (*productionPhaseTwoOwnership, *viewstream.Server, ownership.PublicationAuthority) {
	t.Helper()
	server, err := viewstream.NewServer(viewStreamAdmission{registry: nil, now: time.Now}, nil, viewstream.ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	authority := ownership.PublicationAuthority{Fence: execution.OwnerFence{
		QueryGroup: ownership.ControlLeaderIdentity, OwnerID: "leader", OwnerEpoch: 4, LeaseToken: "token",
	}, Deadline: time.Now().Add(time.Minute)}
	if err := server.Lead(authority.Fence.OwnerEpoch); err != nil {
		t.Fatal(err)
	}
	runtime := &productionPhaseTwoOwnership{authority: authority, dependencies: productionPhaseTwoOwnershipDependencies{
		ViewStream: server, ViewSource: source,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			*observed = append(*observed, observation)
		}),
	}}
	return runtime, server, authority
}

// decision-016 batch 4, section 2 check 3 and section 11 item 1: the entry
// previews the record's timeline revision, and the placement round stamps
// that number onto a record that had not said one without moving its
// record_revision (ownership's publishAssignmentScript; the cutover writes
// it the same way). The view the Worker installs must carry the stamped
// number. A view left at zero answers timeline_unsaid against a lease that
// brings it, and the Query Group is refused until something else in the
// fleet's desired set moves.
func TestATimelineRevisionStampedOntoARecordReachesTheWorkersView(t *testing.T) {
	var observed []observability.Observation
	source := viewPublishSource{published: map[execution.QueryGroupIdentity]controlplane.ContentEntry{"qg-a": {Digest: "obj-a"}}}
	runtime, server, authority := viewPublishRuntime(t, source, &observed)
	ctx := context.Background()
	unsaid := ownership.AssignmentRecord{QueryGroup: "qg-a", DesiredWorkerID: "w1", RecordRevision: 5, ContentScope: "obj-a"}
	runtime.publishView(ctx, authority, map[execution.QueryGroupIdentity]ownership.AssignmentRecord{"qg-a": unsaid}, nil)
	stamped := unsaid
	stamped.TimelineRecordRevision = 9
	runtime.publishView(ctx, authority, map[execution.QueryGroupIdentity]ownership.AssignmentRecord{"qg-a": stamped}, nil)
	if len(observed) != 0 {
		t.Fatalf("observed %+v, want two clean publications", observed)
	}

	view, ok := server.Snapshot("w1")
	if !ok || len(view.Entries) != 1 {
		t.Fatalf("w1 view = %+v (ok=%t), want qg-a", view, ok)
	}
	if got := view.Entries[0].Assignment.TimelineRecordRevision; got != 9 {
		t.Errorf("w1 view at revision %d previews timeline revision %d, the record says 9", view.Version.Revision, got)
	}
	gate := newViewExecutionGate()
	gate.attach(mapView{"qg-a": view.Entries[0]})
	lease := ownership.Lease{ContentScope: "obj-a", TimelineRecordRevision: 9}
	if hint, outcome, _ := gate.judgeAgainstView("qg-a", lease, true); outcome != viewGateExecutable || hint != 9 {
		t.Errorf("gate on the installed view = %s (hint %d) with the lease and the record at 9, want executable", outcome, hint)
	}
}

// decision-016 section 4.1: a current object that is missing makes only
// its own Query Group unexecutable, and nothing may hold the other Query
// Groups' view on it; 02 section 10: a deterministic error ends only its
// own Plan. A draining Query Group whose timeline does not decode is such
// an error, and reads the same every round. It stays in its Worker's view
// without content - the gate answers no_content, as for a draining entry
// with nothing left to run - while every other Query Group is published,
// and the skip is counted and named. The first view of a term takes the
// same path, and before it a Worker that restarted has no view at all.
func TestADrainingTimelineThatDoesNotDecodeLeavesOnlyItsQueryGroupUnexecutable(t *testing.T) {
	records := map[execution.QueryGroupIdentity]ownership.AssignmentRecord{
		"qg-a": {QueryGroup: "qg-a", DesiredWorkerID: "w1", RecordRevision: 5, ContentScope: "obj-a", TimelineRecordRevision: 3},
		"qg-d": {QueryGroup: "qg-d", DesiredWorkerID: "w2", RecordRevision: 5, ContentScope: "obj-d", TimelineRecordRevision: 3},
		"qg-e": {QueryGroup: "qg-e", DesiredWorkerID: "w2", RecordRevision: 5, ContentScope: "obj-e", TimelineRecordRevision: 3},
	}
	source := viewPublishSource{
		published: map[execution.QueryGroupIdentity]controlplane.ContentEntry{"qg-a": {Digest: "obj-a"}},
		draining:  map[execution.QueryGroupIdentity]controlplane.ContentEntry{"qg-e": {Digest: "obj-e"}},
		unreadable: map[execution.QueryGroupIdentity]error{
			"qg-d": &controlplane.DeterministicScheduleError{Err: errors.New("decode Schedule timeline: unexpected end of JSON input")},
		},
		active: []execution.QueryGroupIdentity{"qg-a"},
	}
	ctx := context.Background()
	for name, publish := range map[string]func(*productionPhaseTwoOwnership, ownership.PublicationAuthority){
		"the round's view": func(runtime *productionPhaseTwoOwnership, authority ownership.PublicationAuthority) {
			runtime.publishView(ctx, authority, records, nil)
		},
		"the first view of a term": func(runtime *productionPhaseTwoOwnership, _ ownership.PublicationAuthority) {
			runtime.PublishStoredView(ctx)
		},
	} {
		t.Run(name, func(t *testing.T) {
			var observed []observability.Observation
			runtime, server, authority := viewPublishRuntime(t, source, &observed)
			runtime.dependencies.Store = &storedViewStore{records: records}
			publish(runtime, authority)

			stats := server.Stats()
			if stats.Revision == 0 {
				t.Fatalf("nothing was published (failure %q): qg-a on w1 is not in any view because qg-d on w2 cannot be read", stats.PublishFailureReason)
			}
			w1, _ := server.Snapshot("w1")
			w2, _ := server.Snapshot("w2")
			gate := newViewExecutionGate()
			entries := mapView{}
			for _, entry := range append(append([]viewstream.Entry(nil), w1.Entries...), w2.Entries...) {
				entries[entry.QueryGroup] = entry
			}
			gate.attach(entries)
			for queryGroup, want := range map[execution.QueryGroupIdentity]viewGateOutcome{
				"qg-a": viewGateExecutable, "qg-e": viewGateExecutable, "qg-d": viewGateNoContent,
			} {
				lease := ownership.Lease{ContentScope: records[queryGroup].ContentScope, TimelineRecordRevision: 3}
				if _, outcome, _ := gate.judgeAgainstView(queryGroup, lease, true); outcome != want {
					t.Errorf("%s at the gate = %s, want %s (w1 %+v, w2 %+v)", queryGroup, outcome, want, w1.Entries, w2.Entries)
				}
			}
			// Counted under the reason a failed read has always had, and
			// not a failing stream: the set was published.
			if stats.PublishFailuresByReason[viewstream.PublishFailureDrainingUnreadable] != 1 || stats.PublishFailures != 0 || !stats.PublishFailingSince.IsZero() {
				t.Errorf("stats = %+v, want one draining_unreadable counted and no failing run", stats)
			}
			if len(observed) != 1 || observed[0].Result != observability.ResultDegraded || observed[0].ViewStream == nil ||
				!strings.Contains(observed[0].ViewStream.Reason, "qg-d") {
				t.Errorf("observed %+v, want one degraded line naming qg-d", observed)
			}
		})
	}
}

// The other side of the line: a timeline that cannot be reached - the
// store timing out, a connection refused - says nothing about its Query
// Group, and a set built without it would take a draining Query Group's
// content off its Worker for the length of a store outage. The set is not
// published, the view already published stands, and the failure runs
// until a read succeeds, as it did before.
func TestADrainingTimelineThatCannotBeReachedPublishesNothing(t *testing.T) {
	records := map[execution.QueryGroupIdentity]ownership.AssignmentRecord{
		"qg-a": {QueryGroup: "qg-a", DesiredWorkerID: "w1", RecordRevision: 5, ContentScope: "obj-a", TimelineRecordRevision: 3},
		"qg-d": {QueryGroup: "qg-d", DesiredWorkerID: "w2", RecordRevision: 5, ContentScope: "obj-d", TimelineRecordRevision: 3},
	}
	source := viewPublishSource{
		published:  map[execution.QueryGroupIdentity]controlplane.ContentEntry{"qg-a": {Digest: "obj-a"}},
		draining:   map[execution.QueryGroupIdentity]controlplane.ContentEntry{"qg-d": {Digest: "obj-d"}},
		unreadable: map[execution.QueryGroupIdentity]error{},
	}
	var observed []observability.Observation
	runtime, server, authority := viewPublishRuntime(t, source, &observed)
	ctx := context.Background()
	runtime.publishView(ctx, authority, records, nil)
	before := server.Stats()
	if before.Revision != 1 || len(observed) != 0 {
		t.Fatalf("first publication: stats %+v observed %+v", before, observed)
	}
	source.unreadable["qg-d"] = errors.New("dial tcp 192.0.2.10:6379: i/o timeout")
	changed := map[execution.QueryGroupIdentity]ownership.AssignmentRecord{"qg-a": records["qg-a"], "qg-d": records["qg-d"]}
	moved := changed["qg-a"]
	moved.RecordRevision++
	changed["qg-a"] = moved
	runtime.publishView(ctx, authority, changed, nil)
	after := server.Stats()
	if after.Revision != before.Revision {
		t.Errorf("revision %d -> %d: a set was published without a Query Group the store could not answer for", before.Revision, after.Revision)
	}
	if after.PublishFailures != 1 || after.PublishFailureReason != viewstream.PublishFailureDrainingUnreadable || after.PublishFailingSince.IsZero() {
		t.Errorf("stats = %+v, want a failing run of one, draining_unreadable", after)
	}
	if w2, _ := server.Snapshot("w2"); len(w2.Entries) != 1 || w2.Entries[0].Content == nil || w2.Entries[0].Content.ObjectDigest != "obj-d" {
		t.Errorf("w2 view = %+v, want qg-d with its draining content as last published", w2.Entries)
	}
}
