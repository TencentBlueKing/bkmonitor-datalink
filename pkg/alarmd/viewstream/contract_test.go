// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

func plan(strategy string) execution.PlanIdentity {
	return execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: strategy}
}

func content(object string, strategies ...string) viewstream.Content {
	built := viewstream.Content{ObjectDigest: execution.ObjectDigest(object)}
	for _, strategy := range strategies {
		built.OutputContexts = append(built.OutputContexts, viewstream.OutputContextRef{Plan: plan(strategy), Digest: execution.OutputContextDigest("ctx-" + strategy)})
	}
	return built
}

func desiredAt(publication viewstream.Publication, assign map[string]string, published map[string]viewstream.Content) viewstream.Desired {
	desired := viewstream.Desired{ControlEpoch: 7, Publication: publication,
		Content: map[execution.QueryGroupIdentity]viewstream.Content{}, Assignments: map[execution.QueryGroupIdentity]viewstream.Assignment{}}
	for queryGroup, worker := range assign {
		desired.Assignments[execution.QueryGroupIdentity(queryGroup)] = viewstream.Assignment{DesiredWorkerID: worker, Revision: 3, ContentScope: "scope-" + queryGroup}
	}
	for queryGroup, built := range published {
		desired.Content[execution.QueryGroupIdentity(queryGroup)] = built
	}
	return desired
}

var publicationA = viewstream.Publication{SnapshotRevision: "snap-a", PublicationEpoch: 4, ActivationRecordRevision: 40}

// A Query Group is in exactly the view of the Worker its record names, with
// the content the publication carries; one the publication no longer
// carries is in the view with no content, which is draining; one assigned
// to another Worker is not in the view at all. The digest is over the body
// and independent of the order the entries were given in.
func TestAViewIsTheWorkersOwnQueryGroupsWithTheirPublishedContent(t *testing.T) {
	desired := desiredAt(publicationA,
		map[string]string{"qg-1": "w1", "qg-2": "w1", "qg-3": "w2", "qg-drain": "w1"},
		map[string]viewstream.Content{"qg-1": content("obj-1", "s1"), "qg-2": content("obj-2", "s2", "s3"), "qg-3": content("obj-3", "s4")})
	view, err := desired.Project("w1")
	if err != nil {
		t.Fatal(err)
	}
	if err := view.Validate(); err != nil {
		t.Fatal(err)
	}
	want := []viewstream.Entry{
		{QueryGroup: "qg-1", Content: ptr(content("obj-1", "s1")), Assignment: desired.Assignments["qg-1"]},
		{QueryGroup: "qg-2", Content: ptr(content("obj-2", "s2", "s3")), Assignment: desired.Assignments["qg-2"]},
		{QueryGroup: "qg-drain", Assignment: desired.Assignments["qg-drain"]},
	}
	if !reflect.DeepEqual(view.Entries, want) || view.Publication != publicationA || view.Version.ControlEpoch != 7 {
		t.Fatalf("w1 view = %+v, want entries %+v under %+v", view, want, publicationA)
	}
	other, err := desired.Project("w2")
	if err != nil || len(other.Entries) != 1 || other.Entries[0].QueryGroup != "qg-3" || other.Version.Digest == view.Version.Digest {
		t.Fatalf("w2 view = %+v (%v), want qg-3 alone with its own digest", other, err)
	}
	// A Worker nobody is assigned to has an empty view, and an empty view is
	// well formed.
	nobody, err := desired.Project("w9")
	if err != nil || len(nobody.Entries) != 0 || nobody.Validate() != nil {
		t.Fatalf("unassigned worker view = %+v (%v), want empty and valid", nobody, err)
	}
	// The digest is of the body: the same entries in another order name the
	// same digest, a changed assignment revision another one.
	reordered := append([]viewstream.Entry{want[2], want[0]}, want[1])
	digest, err := viewstream.DigestOf(publicationA, reordered)
	if err != nil || digest != view.Version.Digest {
		t.Fatalf("reordered digest = %s (%v), want %s", digest, err, view.Version.Digest)
	}
	moved := desiredAt(publicationA, map[string]string{"qg-1": "w1"}, map[string]viewstream.Content{"qg-1": content("obj-1", "s1")})
	moved.Assignments["qg-1"] = viewstream.Assignment{DesiredWorkerID: "w1", Revision: 4, ContentScope: "scope-qg-1"}
	single, _ := desiredAt(publicationA, map[string]string{"qg-1": "w1"}, map[string]viewstream.Content{"qg-1": content("obj-1", "s1")}).Project("w1")
	bumped, _ := moved.Project("w1")
	if single.Version.Digest == bumped.Version.Digest {
		t.Fatal("a changed assignment revision must change the digest")
	}
	if workers := desired.Workers(); !reflect.DeepEqual(workers, []string{"w1", "w2"}) {
		t.Fatalf("Workers() = %v, want w1 w2", workers)
	}
}

func ptr(built viewstream.Content) *viewstream.Content { return &built }

// The delta is the difference of the two projections, not the projection
// of the difference: a Query Group newly assigned to the Worker arrives
// with its content although only its assignment changed in the desired
// set, one taken away arrives as a removal, one whose content moved arrives
// as an upsert, and an untouched one does not travel. Applying the delta to
// the base yields exactly the next projection; applying it to anything
// else, or a tampered one, is refused whole.
func TestADeltaIsTheDifferenceOfTheProjectionsAndAppliesOnlyToItsBase(t *testing.T) {
	first := desiredAt(publicationA,
		map[string]string{"qg-1": "w1", "qg-2": "w1", "qg-3": "w2", "qg-4": "w1"},
		map[string]viewstream.Content{"qg-1": content("obj-1", "s1"), "qg-2": content("obj-2", "s2"), "qg-3": content("obj-3", "s3"), "qg-4": content("obj-4", "s4")})
	publicationB := viewstream.Publication{SnapshotRevision: "snap-b", PublicationEpoch: 5, ActivationRecordRevision: 41}
	second := desiredAt(publicationB,
		map[string]string{"qg-1": "w1", "qg-2": "w2", "qg-3": "w1", "qg-4": "w1"},
		map[string]viewstream.Content{"qg-1": content("obj-1", "s1"), "qg-2": content("obj-2", "s2"), "qg-3": content("obj-3", "s3"), "qg-4": content("obj-4b", "s4")})
	before, _ := first.Project("w1")
	after, _ := second.Project("w1")
	before.Version.Revision, after.Version.Revision = 1, 2
	delta, err := viewstream.Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Upserts) != 2 || delta.Upserts[0].QueryGroup != "qg-3" || delta.Upserts[0].Content == nil || delta.Upserts[0].Content.ObjectDigest != "obj-3" ||
		delta.Upserts[1].QueryGroup != "qg-4" || delta.Upserts[1].Content.ObjectDigest != "obj-4b" {
		t.Fatalf("upserts = %+v, want qg-3 arriving with its content and qg-4 with its new content", delta.Upserts)
	}
	if !reflect.DeepEqual(delta.Removed, []execution.QueryGroupIdentity{"qg-2"}) {
		t.Fatalf("removed = %v, want qg-2", delta.Removed)
	}
	if delta.Empty() || delta.Publication != publicationB || delta.Base != before.Version || delta.Target != after.Version {
		t.Fatalf("delta envelope = %+v", delta)
	}
	applied, err := viewstream.Apply(before, delta)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(applied, after) {
		t.Fatalf("applied view = %+v\nwant %+v", applied, after)
	}
	// Not the base: refused, and the installed view is untouched.
	elsewhere := before
	elsewhere.Version.Digest = "other"
	if _, err := viewstream.Apply(elsewhere, delta); !errors.Is(err, viewstream.ErrDeltaBaseMismatch) {
		t.Fatalf("applying to another digest = %v, want ErrDeltaBaseMismatch", err)
	}
	behind := before
	behind.Version.Revision = 0
	if _, err := viewstream.Apply(behind, delta); !errors.Is(err, viewstream.ErrDeltaBaseMismatch) {
		t.Fatalf("applying to an older revision = %v, want ErrDeltaBaseMismatch", err)
	}
	// Tampered: the result does not hash to the target and is refused.
	tampered := delta
	tampered.Upserts = append([]viewstream.Entry(nil), delta.Upserts...)
	tampered.Upserts[1].Content = ptr(content("obj-4c", "s4"))
	if _, err := viewstream.Apply(before, tampered); !errors.Is(err, viewstream.ErrDigestMismatch) {
		t.Fatalf("applying a tampered delta = %v, want ErrDigestMismatch", err)
	}
	// A delta cannot span two steps or two terms.
	skipped := after
	skipped.Version.Revision = 3
	if _, err := viewstream.Diff(before, skipped); err == nil {
		t.Fatal("a delta across two revisions must be refused")
	}
	otherTerm := after
	otherTerm.Version.ControlEpoch = 8
	if _, err := viewstream.Diff(before, otherTerm); err == nil {
		t.Fatal("a delta across two terms must be refused")
	}
}

// One revision serves every Worker: a publication that moves one Worker's
// projection advances the revision for all, the unaffected Worker gets an
// empty delta that installs by receipt, a publication that moves nobody's
// projection is not a revision, a Worker whose last Query Group left is
// projected once more to an empty view, and a Worker more than one step
// behind gets no delta.
func TestThePublisherAdvancesOneRevisionForTheFleetAndKeepsOneStep(t *testing.T) {
	publisher, err := viewstream.NewPublisher(7, func() time.Time { return time.Unix(100, 0) })
	if err != nil {
		t.Fatal(err)
	}
	first := desiredAt(publicationA,
		map[string]string{"qg-1": "w1", "qg-2": "w2", "qg-3": "w3"},
		map[string]viewstream.Content{"qg-1": content("obj-1", "s1"), "qg-2": content("obj-2", "s2"), "qg-3": content("obj-3", "s3")})
	published, err := publisher.Publish(first)
	if err != nil || !published.Changed || published.Version.Revision != 1 || !reflect.DeepEqual(published.Affected, []string{"w1", "w2", "w3"}) {
		t.Fatalf("first publish = %+v (%v)", published, err)
	}
	if again, err := publisher.Publish(first); err != nil || again.Changed || again.Version.Revision != 1 {
		t.Fatalf("republishing the same set = %+v (%v), want no revision", again, err)
	}
	// Only w1's content changes: revision 2 for everyone, w1 affected.
	second := desiredAt(publicationA,
		map[string]string{"qg-1": "w1", "qg-2": "w2", "qg-3": "w3"},
		map[string]viewstream.Content{"qg-1": content("obj-1b", "s1"), "qg-2": content("obj-2", "s2"), "qg-3": content("obj-3", "s3")})
	published, err = publisher.Publish(second)
	if err != nil || !published.Changed || published.Version.Revision != 2 || !reflect.DeepEqual(published.Affected, []string{"w1"}) {
		t.Fatalf("second publish = %+v (%v)", published, err)
	}
	w1v1, _ := first.Project("w1")
	w1v1.Version.Revision = 1
	step, ok := publisher.Step("w1", w1v1.Version)
	if !ok || step.Empty() || len(step.Upserts) != 1 || step.Upserts[0].Content.ObjectDigest != "obj-1b" {
		t.Fatalf("w1 step = %+v ok=%t, want the content change", step, ok)
	}
	w2v1, _ := first.Project("w2")
	w2v1.Version.Revision = 1
	step, ok = publisher.Step("w2", w2v1.Version)
	if !ok || !step.Empty() || step.Target.Revision != 2 || step.Target.Digest != w2v1.Version.Digest {
		t.Fatalf("w2 step = %+v ok=%t, want an empty delta to revision 2 at the same digest", step, ok)
	}
	installed, err := viewstream.Apply(w2v1, step)
	if err != nil || installed.Version.Revision != 2 || !reflect.DeepEqual(installed.Entries, w2v1.Entries) {
		t.Fatalf("applying the empty delta = %+v (%v)", installed, err)
	}
	// w3 never installed revision 1: no step from revision 0, a snapshot.
	if _, ok := publisher.Step("w3", viewstream.Version{ControlEpoch: 7}); ok {
		t.Fatal("a Worker without an installed view must get a snapshot, not a delta")
	}
	snapshot, ok := publisher.Snapshot("w3")
	if !ok || snapshot.Version.Revision != 2 || len(snapshot.Entries) != 1 || snapshot.Validate() != nil {
		t.Fatalf("w3 snapshot = %+v ok=%t", snapshot, ok)
	}
	// qg-3 moves from w3 to w1: w3 is projected once more, to an empty view.
	third := desiredAt(publicationA,
		map[string]string{"qg-1": "w1", "qg-2": "w2", "qg-3": "w1"},
		map[string]viewstream.Content{"qg-1": content("obj-1b", "s1"), "qg-2": content("obj-2", "s2"), "qg-3": content("obj-3", "s3")})
	published, err = publisher.Publish(third)
	if err != nil || published.Version.Revision != 3 || !reflect.DeepEqual(published.Affected, []string{"w1", "w3"}) {
		t.Fatalf("third publish = %+v (%v)", published, err)
	}
	w3v2 := snapshot
	step, ok = publisher.Step("w3", w3v2.Version)
	if !ok || !reflect.DeepEqual(step.Removed, []execution.QueryGroupIdentity{"qg-3"}) || len(step.Upserts) != 0 {
		t.Fatalf("w3 step = %+v ok=%t, want the removal of qg-3", step, ok)
	}
	emptied, err := viewstream.Apply(w3v2, step)
	if err != nil || len(emptied.Entries) != 0 || emptied.Version.Revision != 3 {
		t.Fatalf("w3 after removal = %+v (%v)", emptied, err)
	}
	// w2 is two steps behind now (installed 1, current 3): a snapshot.
	if _, ok := publisher.Step("w2", w2v1.Version); ok {
		t.Fatal("a Worker two revisions behind must get a snapshot, not a delta")
	}
	// A fourth publication no longer projects w3; asking for it gives the
	// empty view at the current revision, valid.
	fourth := third
	fourth.Publication = viewstream.Publication{SnapshotRevision: "snap-c", PublicationEpoch: 6, ActivationRecordRevision: 42}
	if published, err = publisher.Publish(fourth); err != nil || published.Version.Revision != 4 || !reflect.DeepEqual(published.Affected, []string{"w1", "w2"}) {
		t.Fatalf("fourth publish = %+v (%v): w3 holds nothing and is not projected again", published, err)
	}
	idle, ok := publisher.Snapshot("w3")
	if !ok || idle.Version.Revision != 4 || len(idle.Entries) != 0 || idle.Validate() != nil || idle.Publication != fourth.Publication {
		t.Fatalf("idle worker snapshot = %+v ok=%t (%v)", idle, ok, idle.Validate())
	}
	// Another term's desired set is refused: a term is a publisher.
	otherTerm := fourth
	otherTerm.ControlEpoch = 8
	if _, err := publisher.Publish(otherTerm); err == nil {
		t.Fatal("a desired set of another term must be refused")
	}
}

// The four numbers of a version: each receiver once per stage in order,
// gated by the stage before; a receipt asserting a later stage without the
// earlier moves nothing the earlier gates; a receipt for a digest the
// receiver's view does not have, from a receiver the version did not
// expect, from a superseded incarnation, or for a version no longer
// followed, is counted as ignored and changes nothing; a restart under a
// version voids the old process's stages; a version is complete only when
// every expected receiver switched.
func TestTheLedgerCountsEachReceiverOncePerStageInOrder(t *testing.T) {
	now := time.Unix(100, 0)
	ledger := viewstream.NewLedger(func() time.Time { return now })
	v1 := viewstream.Key{ControlEpoch: 7, Revision: 1}
	ledger.Open(v1, map[string]string{"w1": "d1", "w2": "d2", "w3": "d3"})
	counts, ok := ledger.Counts(v1)
	if !ok || counts != (viewstream.Counts{Expected: 3}) {
		t.Fatalf("opened counts = %+v ok=%t", counts, ok)
	}
	w1 := viewstream.Receiver{WorkerID: "w1", Incarnation: "i1"}
	version := func(worker, digest string) viewstream.Version {
		return viewstream.Version{ControlEpoch: 7, Revision: 1, Digest: digest}
	}
	// installed asserted without acked: nothing acked gates moves.
	ledger.MarkSent(v1, w1)
	ledger.Record(viewstream.Receipt{Receiver: w1, Version: version("w1", "d1"), Installed: true})
	if counts, _ = ledger.Counts(v1); counts != (viewstream.Counts{Expected: 3, Sent: 1}) {
		t.Fatalf("installed without acked = %+v, want sent only", counts)
	}
	// The complete facts arrive: acked and installed count, once, however
	// often they are repeated.
	for i := 0; i < 3; i++ {
		ledger.Record(viewstream.Receipt{Receiver: w1, Version: version("w1", "d1"), Acked: true, Installed: true, ObjectsMissing: 2})
	}
	if counts, _ = ledger.Counts(v1); counts != (viewstream.Counts{Expected: 3, Sent: 1, Acked: 1, Installed: 1}) {
		t.Fatalf("after complete receipts = %+v", counts)
	}
	// The object count of an installed receiver is its latest word, one way
	// or the other. Three states in a row: a probe that found 3 missing; a
	// probe that failed, which takes the receiver out of the sum and names it
	// -- the 3 it found earlier is not a count now, and reading it on made
	// "cannot tell" look like "3 missing" (or, at 0, "nothing missing") for
	// as long as the probe kept failing; and a probe that succeeded again.
	// Only the claim at a Hello, which cannot speak for the objects, leaves
	// the word in hand where it is.
	ledger.Record(viewstream.Receipt{Receiver: w1, Version: version("w1", "d1"), Acked: true, Installed: true, ObjectsMissing: 3, ObjectsProbed: true})
	if objects, ok := ledger.Objects(v1); !ok || !reflect.DeepEqual(objects, viewstream.ObjectsSummary{Missing: 3, Probed: 1}) {
		t.Fatalf("objects after a probed receipt = %+v ok=%t, want 3 missing over one probed receiver", objects, ok)
	}
	ledger.Record(viewstream.Receipt{Receiver: w1, Version: version("w1", "d1"), Acked: true, Installed: true, ObjectsProbed: false})
	if objects, _ := ledger.Objects(v1); !reflect.DeepEqual(objects, viewstream.ObjectsSummary{Unprobed: 1, UnprobedWorkers: []string{"w1"}}) {
		t.Fatalf("objects after a failed probe = %+v, want the receiver unprobed and named, its earlier count gone", objects)
	}
	ledger.RecordClaimed(w1, version("w1", "d1"))
	if objects, _ := ledger.Objects(v1); !reflect.DeepEqual(objects, viewstream.ObjectsSummary{Unprobed: 1, UnprobedWorkers: []string{"w1"}}) {
		t.Fatalf("objects after a Hello claim = %+v, want the failed probe left where it was", objects)
	}
	ledger.Record(viewstream.Receipt{Receiver: w1, Version: version("w1", "d1"), Acked: true, Installed: true, ObjectsMissing: 0, ObjectsProbed: true})
	if objects, _ := ledger.Objects(v1); !reflect.DeepEqual(objects, viewstream.ObjectsSummary{Missing: 0, Probed: 1}) {
		t.Fatalf("objects after the probe succeeds again = %+v, want one probed receiver with nothing missing", objects)
	}
	ledger.Record(viewstream.Receipt{Receiver: w1, Version: version("w1", "d1"), Acked: true, Installed: true, ObjectsMissing: 3, ObjectsProbed: true})
	ledger.RecordClaimed(w1, version("w1", "d1"))
	if objects, _ := ledger.Objects(v1); !reflect.DeepEqual(objects, viewstream.ObjectsSummary{Missing: 3, Probed: 1}) {
		t.Fatalf("objects after a Hello claim over a probed count = %+v, want the count kept", objects)
	}
	// A receipt before sent: acked is recorded but not counted until sent.
	w2 := viewstream.Receiver{WorkerID: "w2", Incarnation: "i2"}
	ledger.Record(viewstream.Receipt{Receiver: w2, Version: version("w2", "d2"), Acked: true})
	if counts, _ = ledger.Counts(v1); counts.Acked != 1 {
		t.Fatalf("acked before sent counted: %+v", counts)
	}
	ledger.MarkSent(v1, w2)
	if counts, _ = ledger.Counts(v1); counts != (viewstream.Counts{Expected: 3, Sent: 2, Acked: 2, Installed: 1}) {
		t.Fatalf("after w2 sent = %+v", counts)
	}
	// Ignored, each for its own reason, none counted.
	ledger.Record(viewstream.Receipt{Receiver: w2, Version: version("w2", "d-other"), Acked: true, Installed: true})
	ledger.Record(viewstream.Receipt{Receiver: viewstream.Receiver{WorkerID: "w9", Incarnation: "i9"}, Version: version("w9", "d9"), Acked: true})
	ledger.Record(viewstream.Receipt{Receiver: viewstream.Receiver{WorkerID: "w2", Incarnation: "i2-old"}, Version: version("w2", "d2"), Acked: true, Installed: true})
	ledger.Record(viewstream.Receipt{Receiver: w1, Version: viewstream.Version{ControlEpoch: 7, Revision: 9, Digest: "d1"}, Acked: true})
	if got := ledger.Ignored(); got != (viewstream.Ignored{DigestMismatch: 1, UnexpectedReceiver: 1, StaleIncarnation: 1, UnknownVersion: 1}) {
		t.Fatalf("ignored = %+v", got)
	}
	if counts, _ = ledger.Counts(v1); counts != (viewstream.Counts{Expected: 3, Sent: 2, Acked: 2, Installed: 1}) {
		t.Fatalf("ignored receipts moved the counts: %+v", counts)
	}
	// w1 restarts under this version: its stages are void, the new process
	// starts from sent.
	ledger.MarkSent(v1, viewstream.Receiver{WorkerID: "w1", Incarnation: "i1b"})
	if counts, _ = ledger.Counts(v1); counts != (viewstream.Counts{Expected: 3, Sent: 2, Acked: 1, Installed: 0}) {
		t.Fatalf("after w1 restarted = %+v, want its acked and installed gone", counts)
	}
	lagging := ledger.Lagging("installed")
	if len(lagging) != 3 || lagging[0].WorkerID != "w1" || lagging[0].Incarnation != "i1b" || lagging[2].WorkerID != "w3" || lagging[2].Incarnation != "" {
		t.Fatalf("lagging installed = %+v", lagging)
	}
	// Everyone installs, then switches: the receipt that completes the
	// installed stage for the last expected receiver says so once, with
	// the time from publication; later receipts do not say it again.
	now = now.Add(7 * time.Second)
	var completions []viewstream.Recorded
	for _, receiver := range []viewstream.Receiver{{WorkerID: "w1", Incarnation: "i1b"}, w2, {WorkerID: "w3", Incarnation: "i3"}} {
		ledger.MarkSent(v1, receiver)
		recorded := ledger.Record(viewstream.Receipt{Receiver: receiver, Version: version(receiver.WorkerID, "d"+receiver.WorkerID[1:]), Acked: true, Installed: true, Switched: true})
		if !recorded.Attributed {
			t.Fatalf("receipt from %s not attributed", receiver.WorkerID)
		}
		if recorded.InstalledByAll {
			completions = append(completions, recorded)
		}
	}
	if len(completions) != 1 || completions[0].Version != v1 || completions[0].Expected != 3 || completions[0].Elapsed != 7*time.Second {
		t.Fatalf("installed-by-all completions = %+v, want one for v1 with three expected after seven seconds", completions)
	}
	if again := ledger.Record(viewstream.Receipt{Receiver: w2, Version: version("w2", "d2"), Acked: true, Installed: true, Switched: true}); again.InstalledByAll || !again.Attributed {
		t.Fatalf("a repeat after completion = %+v, want attributed and not completing again", again)
	}
	if counts, _ = ledger.Counts(v1); !counts.Complete() || counts != (viewstream.Counts{Expected: 3, Sent: 3, Acked: 3, Installed: 3, Switched: 3}) {
		t.Fatalf("after everyone switched = %+v", counts)
	}
	// w1 restarted after its probed receipt, so its count went with the old
	// process; nobody in the final round probed: three unprobed, sum 0 --
	// and the sum is not read as three fleets with their objects.
	if objects, _ := ledger.Objects(v1); !reflect.DeepEqual(objects, viewstream.ObjectsSummary{Missing: 0, Probed: 0, Unprobed: 3, UnprobedWorkers: []string{"w1", "w2", "w3"}}) {
		t.Fatalf("objects at completion = %+v, want three unprobed, named, and no sum", objects)
	}
	// Two more versions: the first is closed complete, the second, opened
	// and never reported, is closed superseded with its counts, and neither
	// is followed after.
	v2 := viewstream.Key{ControlEpoch: 7, Revision: 2}
	v3 := viewstream.Key{ControlEpoch: 7, Revision: 3}
	if closed := ledger.Open(v2, map[string]string{"w1": "e1", "w2": "e2"}); len(closed) != 0 {
		t.Fatalf("opening the second version closed %+v, want nothing: the first is still followed", closed)
	}
	ledger.MarkSent(v2, w2)
	now = now.Add(time.Minute)
	closed := ledger.Open(v3, map[string]string{"w1": "f1", "w2": "f2"})
	if len(closed) != 1 || closed[0].Version != v1 || closed[0].Reason != "" || !closed[0].Counts.Complete() || closed[0].ClosedAt != now {
		t.Fatalf("closing the complete version = %+v", closed)
	}
	if _, ok := ledger.Counts(v1); ok {
		t.Fatal("a closed version must not be followed")
	}
	now = now.Add(time.Minute)
	closed = ledger.Open(viewstream.Key{ControlEpoch: 7, Revision: 4}, map[string]string{"w1": "g1"})
	if len(closed) != 1 || closed[0].Version != v2 || closed[0].Reason != "superseded" || closed[0].Counts != (viewstream.Counts{Expected: 2, Sent: 1}) {
		t.Fatalf("closing the superseded version = %+v", closed)
	}
	current, counts, ok := ledger.Current()
	if !ok || current.Revision != 4 || counts != (viewstream.Counts{Expected: 1}) {
		t.Fatalf("current = %+v %+v ok=%t", current, counts, ok)
	}
}

// The fingerprint is what lets the publisher skip a round that changed
// nothing, so it must move whenever any projection would: every field a
// view is built from is covered, and two builds of one set agree.
func TestTheFingerprintMovesWheneverAProjectionWould(t *testing.T) {
	base := func() viewstream.Desired {
		return desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-2": "w2"},
			map[string]viewstream.Content{"qg-1": content("obj-1", "s1"), "qg-2": content("obj-2", "s2")})
	}
	if base().Fingerprint() != base().Fingerprint() {
		t.Fatal("two builds of one desired set must have one fingerprint")
	}
	// The same output contexts in another order are the same content: the
	// digest sorts them, and so must the fingerprint, or a round that hands
	// the refs over in map order would look changed every time and the
	// skip would never happen.
	reordered := base()
	reordered.Content["qg-2"] = content("obj-2", "s9", "s2")
	ordered := base()
	ordered.Content["qg-2"] = content("obj-2", "s2", "s9")
	if reordered.Fingerprint() != ordered.Fingerprint() {
		t.Fatal("output contexts in another order changed the fingerprint")
	}
	was, _ := ordered.Project("w2")
	is, _ := reordered.Project("w2")
	if was.Version.Digest != is.Version.Digest {
		t.Fatal("output contexts in another order changed the digest; the fingerprint case above proves nothing")
	}
	for name, mutate := range map[string]func(*viewstream.Desired){
		"publication snapshot": func(d *viewstream.Desired) { d.Publication.SnapshotRevision = "snap-x" },
		"publication epoch":    func(d *viewstream.Desired) { d.Publication.PublicationEpoch++ },
		"activation record":    func(d *viewstream.Desired) { d.Publication.ActivationRecordRevision++ },
		"assignment worker": func(d *viewstream.Desired) {
			a := d.Assignments["qg-1"]
			a.DesiredWorkerID = "w2"
			d.Assignments["qg-1"] = a
		},
		"assignment revision": func(d *viewstream.Desired) { a := d.Assignments["qg-1"]; a.Revision++; d.Assignments["qg-1"] = a },
		"assignment scope": func(d *viewstream.Desired) {
			a := d.Assignments["qg-1"]
			a.ContentScope = "other"
			d.Assignments["qg-1"] = a
		},
		"assignment pending": func(d *viewstream.Desired) {
			a := d.Assignments["qg-1"]
			a.PendingContentScope = "p"
			a.EffectiveAtMs = 5
			d.Assignments["qg-1"] = a
		},
		"content digest":        func(d *viewstream.Desired) { d.Content["qg-1"] = content("obj-1b", "s1") },
		"content ref":           func(d *viewstream.Desired) { d.Content["qg-1"] = content("obj-1", "s1b") },
		"content gone/draining": func(d *viewstream.Desired) { delete(d.Content, "qg-1") },
		"query group gone":      func(d *viewstream.Desired) { delete(d.Assignments, "qg-2") },
	} {
		mutated := base()
		mutate(&mutated)
		before, after := base(), mutated
		movedProjection := false
		for _, worker := range []string{"w1", "w2"} {
			was, _ := before.Project(worker)
			is, _ := after.Project(worker)
			if was.Version.Digest != is.Version.Digest {
				movedProjection = true
			}
		}
		if !movedProjection {
			t.Fatalf("%s: the mutation moved no projection; the case proves nothing", name)
		}
		if before.Fingerprint() == after.Fingerprint() {
			t.Fatalf("%s: a projection moved and the fingerprint did not; the publisher would skip a real change", name)
		}
	}
}
