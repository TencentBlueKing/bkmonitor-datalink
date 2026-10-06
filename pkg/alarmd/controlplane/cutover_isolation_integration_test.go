// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A cutover whose preconditions fail on one Query Group used to fail the
// whole publication, and every later one at the same place. These pin that
// the Query Group alone is held back and the rest of the publication goes
// ahead, that it is judged again at every cutover until it passes, and that
// a timeline whose key is gone is opened again rather than stopping every
// publication.

func (fixture *cutoverFixture) rewriteOpenDigest(t *testing.T, group execution.QueryGroupIdentity, digest execution.ObjectDigest) []byte {
	t.Helper()
	original := fixture.timelineBytes(t, group)
	var timeline map[string]any
	if err := json.Unmarshal(original, &timeline); err != nil {
		t.Fatal(err)
	}
	segments := timeline["segments"].([]any)
	open := segments[len(segments)-1].(map[string]any)["schedule"].(map[string]any)["Segment"].(map[string]any)
	open["ObjectDigest"] = string(digest)
	rewritten, err := json.Marshal(timeline)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.client.Set(fixture.ctx, fixture.prefix+":schedule_timeline:"+string(group), rewritten, 0).Err(); err != nil {
		t.Fatal(err)
	}
	return original
}

func (fixture *cutoverFixture) publishNoEnsure(t *testing.T, catalog controlplane.Catalog, at int64) (controlplane.PublishedSnapshot, error) {
	t.Helper()
	*fixture.now = time.Unix(at, 0)
	snapshot, _, err := fixture.repository.PublishCatalog(fixture.ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.reconciler.Ensure(fixture.ctx, snapshot.Publication)
	return snapshot, err
}

func (fixture *cutoverFixture) blocked(t *testing.T) []controlplane.BlockedQueryGroup {
	t.Helper()
	groups, err := fixture.repository.ActivationBlocked(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return groups
}

func digestOf(t *testing.T, group controlplane.QueryGroup) execution.ObjectDigest {
	t.Helper()
	digest, err := controlplane.DeriveQueryGroupObjectDigest(group)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

// The production shape: something outside the cutover rewrote one Query
// Group's open Segment. That Query Group keeps its records and its timeline
// as they were; the edited one is cut to its new content; the activation
// advances and names the held-back one. The next publication judges it
// again, keeping when the block began; once the timeline is repaired, the
// block clears.
func TestAQueryGroupWithARewrittenSegmentIsHeldBackAndTheRestActivates(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-isolation")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	edited, untouched := splitEdited(t, first, second)
	fixture.publish(t, first, 60)
	before := fixture.activation(t)
	recordsUntouched := recordsOf(before, untouched)
	activatedDigest := digestOf(t, untouched)
	original := fixture.rewriteOpenDigest(t, untouched.Identity, digestOf(t, edited))
	tampered := fixture.timelineBytes(t, untouched.Identity)

	secondSnapshot, err := fixture.publishNoEnsure(t, second, 120)
	if err != nil {
		t.Fatalf("one Query Group's rewritten Segment failed the whole publication: %v", err)
	}
	after := fixture.activation(t)
	if after.Current != secondSnapshot.Publication {
		t.Fatalf("activation current=%+v, want the new publication %+v", after.Current, secondSnapshot.Publication)
	}
	if cut := fixture.openSegment(t, edited.Identity, 120); cut.Start != 120 || cut.Publication.SnapshotRevision != secondSnapshot.Publication.SnapshotRevision {
		t.Fatalf("the edited Query Group was not cut to the new content: %+v", cut)
	}
	if got := recordsOf(after, untouched); !reflect.DeepEqual(sortedRecords(got), sortedRecords(recordsUntouched)) {
		t.Fatalf("the held-back Query Group's records changed:\n before=%+v\n after=%+v", recordsUntouched, got)
	}
	if got := fixture.timelineBytes(t, untouched.Identity); !reflect.DeepEqual(got, tampered) {
		t.Fatal("the held-back Query Group's timeline was written")
	}
	blocked := fixture.blocked(t)
	if len(blocked) != 1 || blocked[0].QueryGroup != untouched.Identity || blocked[0].Reason != controlplane.CutoverReasonOpenDigestMismatch ||
		blocked[0].ActivatedDigest != activatedDigest || blocked[0].OpenDigest != digestOf(t, edited) || blocked[0].Since != 120 {
		t.Fatalf("blocked = %+v", blocked)
	}
	if after.BlockedCount != 1 || after.BlockedDigest == "" {
		t.Fatalf("the activation does not account for the held-back set: count=%d digest=%q", after.BlockedCount, after.BlockedDigest)
	}

	// Judged again at the next cutover even though its content did not move:
	// still held, and since is kept.
	third := cutoverCatalog(t, 95, nil)
	if _, err := fixture.publishNoEnsure(t, third, 180); err != nil {
		t.Fatalf("third publication: %v", err)
	}
	if blocked := fixture.blocked(t); len(blocked) != 1 || blocked[0].Since != 120 || blocked[0].ActivatedDigest != activatedDigest {
		t.Fatalf("the held-back Query Group was not judged again, or lost when it began: %+v", blocked)
	}

	// Repaired: the next cutover finds the precondition holding and the
	// content unchanged, and the block clears.
	if err := fixture.client.Set(fixture.ctx, fixture.prefix+":schedule_timeline:"+string(untouched.Identity), original, 0).Err(); err != nil {
		t.Fatal(err)
	}
	fourth := cutoverCatalog(t, 85, nil)
	if _, err := fixture.publishNoEnsure(t, fourth, 240); err != nil {
		t.Fatalf("fourth publication: %v", err)
	}
	if blocked := fixture.blocked(t); len(blocked) != 0 {
		t.Fatalf("the repaired Query Group is still held back: %+v", blocked)
	}
	if state := fixture.activation(t); state.BlockedCount != 0 || state.BlockedDigest != "" {
		t.Fatalf("the activation still counts a held-back set: %+v", state)
	}
	if exists, _ := fixture.client.Exists(fixture.ctx, fixture.prefix+":activation_blocked").Result(); exists != 0 {
		t.Fatal("the empty held-back set was left behind")
	}
}

// The running content, as the view and the content scopes read it, gives
// the held-back Query Group what its open Segment names - not the
// manifest's new content, which would stop it on a scope mismatch or run it
// against records that do not match.
func TestTheRunningContentGivesAHeldBackQueryGroupItsOpenSegment(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-isolation-view")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	edited, untouched := splitEdited(t, first, second)
	fixture.publish(t, first, 60)
	fixture.rewriteOpenDigest(t, untouched.Identity, digestOf(t, edited))
	snapshot, err := fixture.publishNoEnsure(t, second, 120)
	if err != nil {
		t.Fatal(err)
	}
	published, err := fixture.repository.LoadPublishedContent(fixture.ctx, snapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest := published.Groups[untouched.Identity].Digest
	running := controlplane.ApplyBlockedToContent(published.Groups, fixture.blocked(t))
	if running[untouched.Identity].Digest != digestOf(t, edited) {
		t.Fatalf("running content for the held-back Query Group = %s, want its open Segment's %s", running[untouched.Identity].Digest, digestOf(t, edited))
	}
	if published.Groups[untouched.Identity].Digest != manifestDigest {
		t.Fatal("applying the held-back set changed the remembered content")
	}
	if running[edited.Identity].Digest != published.Groups[edited.Identity].Digest {
		t.Fatal("a Query Group that was not held back does not run the manifest's content")
	}
}

// A timeline whose key is gone - evicted, expired - used to fail every
// publication as a dependency that did not answer. It is opened again as
// for a new Query Group.
func TestATimelineWhoseKeyIsGoneIsOpenedAgain(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-reopen")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	_, untouched := splitEdited(t, first, second)
	fixture.publish(t, first, 60)
	if err := fixture.client.Del(fixture.ctx, fixture.prefix+":schedule_timeline:"+string(untouched.Identity)).Err(); err != nil {
		t.Fatal(err)
	}
	firstRecords := recordsOf(fixture.activation(t), untouched)
	if _, err := fixture.publishNoEnsure(t, second, 120); err != nil {
		t.Fatalf("a timeline whose key was gone failed the publication: %v", err)
	}
	// Its content did not change, so its records stay as they were and the
	// new Segment names the publication they were activated under.
	opened := fixture.openSegment(t, untouched.Identity, 120)
	if opened.Start != 120 || opened.End != nil || opened.Publication.SnapshotRevision != firstRecords[0].Publication.SnapshotRevision {
		t.Fatalf("the Query Group was not opened again at the boundary under its records' publication: %+v", opened)
	}
	if got := recordsOf(fixture.activation(t), untouched); !reflect.DeepEqual(sortedRecords(got), sortedRecords(firstRecords)) {
		t.Fatalf("the reopened Query Group's records changed:\n before=%+v\n after=%+v", firstRecords, got)
	}
	if blocked := fixture.blocked(t); len(blocked) != 0 {
		t.Fatalf("a reopened Query Group was held back: %+v", blocked)
	}
	if _, _, reopened := fixture.repository.ActivationBlockedCounts(); reopened != 1 {
		t.Fatalf("reopened = %d, want 1", reopened)
	}
}

// The key read as gone and appearing again before the write - a read that
// landed on a lagging replica - is a conflict, and nothing is written: the
// script opens a timeline only where the key is still absent.
func TestATimelineThatReappearsBeforeTheWriteIsNotOverwritten(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-reopen-race")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	_, untouched := splitEdited(t, first, second)
	fixture.publish(t, first, 60)
	key := fixture.prefix + ":schedule_timeline:" + string(untouched.Identity)
	original := fixture.timelineBytes(t, untouched.Identity)
	if err := fixture.client.Del(fixture.ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	other := redis.NewClient(fixture.client.Options())
	t.Cleanup(func() { _ = other.Close() })
	var armed atomic.Bool
	armed.Store(true)
	fixture.client.AddHook(reappearHook{armed: &armed, restore: func(ctx context.Context) {
		_ = other.Set(ctx, key, original, 0).Err()
	}})
	before := fixture.activation(t)
	_, err := fixture.publishNoEnsure(t, second, 120)
	if !errors.Is(err, controlplane.ErrActivationConflict) {
		t.Fatalf("err = %v, want a conflict", err)
	}
	if got := fixture.timelineBytes(t, untouched.Identity); !reflect.DeepEqual(got, original) {
		t.Fatal("the timeline that reappeared was overwritten")
	}
	if after := fixture.activation(t); after.RecordRevision != before.RecordRevision {
		t.Fatal("the activation moved on a conflict")
	}
}

// reappearHook puts a key back just before the cutover script runs, once.
type reappearHook struct {
	armed   *atomic.Bool
	restore func(context.Context)
}

func (hook reappearHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if cmd.Name() == "eval" && len(cmd.Args()) > 1 && strings.Contains(fmt.Sprint(cmd.Args()[1]), "set of Query Groups this cutover held back") &&
		hook.armed.CompareAndSwap(true, false) {
		hook.restore(ctx)
	}
	return ctx, nil
}
func (reappearHook) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (reappearHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (reappearHook) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

// The held-back set lost - the key evicted - is noticed against the
// activation body, and the cutover reads every timeline: the held-back Query
// Group is found and held again rather than passed off as unchanged.
func TestALostHeldBackSetIsNoticedAndTheQueryGroupIsJudgedAgain(t *testing.T) {
	for _, shape := range []string{"key_lost", "body_rebuilt"} {
		t.Run(shape, func(t *testing.T) {
			fixture := newCutoverFixture(t, "alarmd:control:cutover-isolation-lost-"+shape)
			first := cutoverCatalog(t, 80, nil)
			second := cutoverCatalog(t, 90, nil)
			edited, untouched := splitEdited(t, first, second)
			fixture.publish(t, first, 60)
			fixture.rewriteOpenDigest(t, untouched.Identity, digestOf(t, edited))
			if _, err := fixture.publishNoEnsure(t, second, 120); err != nil {
				t.Fatal(err)
			}
			want := controlplane.BlockedSetLost
			switch shape {
			case "key_lost":
				if err := fixture.client.Del(fixture.ctx, fixture.prefix+":activation_blocked").Err(); err != nil {
					t.Fatal(err)
				}
			case "body_rebuilt":
				// A body written by a leader that does not know the set, or
				// rebuilt from the timelines, does not count it.
				raw, err := fixture.client.Get(fixture.ctx, fixture.prefix+":activation").Bytes()
				if err != nil {
					t.Fatal(err)
				}
				var body map[string]any
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Fatal(err)
				}
				delete(body, "blocked_count")
				delete(body, "blocked_digest")
				rewritten, _ := json.Marshal(body)
				if err := fixture.client.Set(fixture.ctx, fixture.prefix+":activation", rewritten, 0).Err(); err != nil {
					t.Fatal(err)
				}
				fixture.repository.ForgetActivationCacheForTest()
				want = controlplane.BlockedSetUnaccounted
			}
			third := cutoverCatalog(t, 95, nil)
			if _, err := fixture.publishNoEnsure(t, third, 180); err != nil {
				t.Fatal(err)
			}
			if blocked := fixture.blocked(t); len(blocked) != 1 || blocked[0].QueryGroup != untouched.Identity {
				t.Fatalf("after the set was %s the held-back Query Group was not judged again: %+v", want, blocked)
			}
			if _, accounting, _ := fixture.repository.ActivationBlockedCounts(); accounting[want] != 1 {
				t.Fatalf("accounting = %v, want one %s", accounting, want)
			}
		})
	}
}

// Renewal keeps what a held-back Query Group runs alive: its open Segment
// names content the new manifest no longer does, and renewing only the
// manifest's objects would let it expire a catalog TTL later.
func TestRenewalKeepsWhatAHeldBackQueryGroupRuns(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-isolation-renew")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	edited, untouched := splitEdited(t, first, second)
	fixture.publish(t, first, 60)
	oldEdited := digestOf(t, edited)
	fixture.rewriteOpenDigest(t, untouched.Identity, oldEdited)
	if _, err := fixture.publishNoEnsure(t, second, 120); err != nil {
		t.Fatal(err)
	}
	key := fixture.prefix + ":qgobj:" + string(oldEdited)
	if err := fixture.client.PExpire(fixture.ctx, key, 10*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.repository.RenewCurrentActivationObjects(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if ttl, err := fixture.client.PTTL(fixture.ctx, key).Result(); err != nil || ttl < 30*time.Minute {
		t.Fatalf("the held-back Query Group's object ttl = %s (%v), want renewed", ttl, err)
	}
}

// A timeline that could not be read - the store did not answer - is not a
// timeline that is gone: the cutover fails as before and tries again, and
// nothing is reopened or held back.
func TestATimelineReadThatFailsIsNotTakenForAMissingKey(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-isolation-io")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	_, untouched := splitEdited(t, first, second)
	fixture.publish(t, first, 60)
	key := fixture.prefix + ":schedule_timeline:" + string(untouched.Identity)
	original := fixture.timelineBytes(t, untouched.Identity)
	before := fixture.activation(t)
	var armed atomic.Bool
	armed.Store(true)
	fixture.client.AddHook(failingGetHook{key: key, armed: &armed})
	if _, err := fixture.publishNoEnsure(t, second, 120); err == nil {
		t.Fatal("a timeline read that failed was not a failed publication")
	}
	armed.Store(false)
	if got := fixture.timelineBytes(t, untouched.Identity); !reflect.DeepEqual(got, original) {
		t.Fatal("the timeline was written after a failed read")
	}
	if after := fixture.activation(t); after.RecordRevision != before.RecordRevision {
		t.Fatal("the activation moved after a failed read")
	}
	if blocked := fixture.blocked(t); len(blocked) != 0 {
		t.Fatalf("a failed read held the Query Group back: %+v", blocked)
	}
}

type failingGetHook struct {
	key   string
	armed *atomic.Bool
}

func (hook failingGetHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if hook.armed.Load() && cmd.Name() == "get" && len(cmd.Args()) > 1 && fmt.Sprint(cmd.Args()[1]) == hook.key {
		return ctx, errors.New("i/o timeout")
	}
	return ctx, nil
}
func (failingGetHook) AfterProcess(context.Context, redis.Cmder) error { return nil }

// A cutover reads its timelines in pipelined batches; the read fails there
// the same way, taking the batch with it as a connection failure would.
func (hook failingGetHook) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	for _, cmd := range cmds {
		if _, err := hook.BeforeProcess(ctx, cmd); err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}
func (failingGetHook) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

// A Query Group leaving the publication with a rewritten timeline has nothing
// to keep running: it leaves the activation, its timeline is not written, and
// it is not held back.
func TestALeavingQueryGroupWithARewrittenTimelineIsRetiredUnwritten(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-isolation-leaving")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	edited, untouched := splitEdited(t, first, second)
	fixture.publish(t, first, 60)
	fixture.rewriteOpenDigest(t, untouched.Identity, digestOf(t, edited))
	tampered := fixture.timelineBytes(t, untouched.Identity)
	leaving := catalogWithout(t, second, untouched.Identity)
	if _, err := fixture.publishNoEnsure(t, leaving, 120); err != nil {
		t.Fatalf("a leaving Query Group with a rewritten timeline failed the publication: %v", err)
	}
	if got := fixture.timelineBytes(t, untouched.Identity); !reflect.DeepEqual(got, tampered) {
		t.Fatal("the leaving Query Group's rewritten timeline was written")
	}
	if blocked := fixture.blocked(t); len(blocked) != 0 {
		t.Fatalf("a leaving Query Group was held back: %+v", blocked)
	}
	if records := recordsOf(fixture.activation(t), untouched); len(records) != 0 {
		t.Fatalf("the leaving Query Group's records stayed: %+v", records)
	}
}

// catalogWithout is a catalog built from only the strategy whose Query Group
// is not the named one, at the edited threshold.
func catalogWithout(t *testing.T, catalog controlplane.Catalog, group execution.QueryGroupIdentity) controlplane.Catalog {
	t.Helper()
	documents := realThresholdDocuments(t)
	documentA := []byte(strings.Replace(string(documents[0]), `"threshold":80`, `"threshold":90`, 1))
	var decodedB map[string]any
	if err := json.Unmarshal(withWireIdentity(t, documents[1], "tenant-a", "bkcc__3"), &decodedB); err != nil {
		t.Fatal(err)
	}
	decodedB["bk_biz_id"] = float64(3)
	documentB, err := json.Marshal(decodedB)
	if err != nil {
		t.Fatal(err)
	}
	planner := &businessPlanner{facts: map[string]execution.QueryPlanFacts{"2": queryFactsFor(t, "2", "bkcc__2"), "3": queryFactsFor(t, "3", "bkcc__3")}}
	for _, source := range []controlplane.SourceStrategy{
		{SourceID: "1001", Document: documentA, Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}},
		{SourceID: "1002", Document: documentB, Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "3", SpaceScope: "bkcc__3"}},
	} {
		built, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{source}, Planner: planner})
		if err != nil {
			t.Fatal(err)
		}
		if len(built.QueryGroups) == 1 && built.QueryGroups[0].Identity != group {
			return built
		}
	}
	t.Fatalf("no single-strategy catalog leaves %s out", group)
	return controlplane.Catalog{}
}

// A Query Group held back while its own content changed is judged against
// the content its records were activated with, not the manifest's: once its
// timeline is repaired, the next publication cuts it to the new content.
// Judged against the manifest, the repaired Segment would read as rewritten
// and the Query Group would stay held back for good.
func TestAHeldBackQueryGroupWhoseContentChangedIsCutOnceRepaired(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-isolation-changed")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	edited, untouched := splitEdited(t, first, second)
	fixture.publish(t, first, 60)
	original := fixture.rewriteOpenDigest(t, edited.Identity, digestOf(t, untouched))
	if _, err := fixture.publishNoEnsure(t, second, 120); err != nil {
		t.Fatal(err)
	}
	if blocked := fixture.blocked(t); len(blocked) != 1 || blocked[0].QueryGroup != edited.Identity || blocked[0].ActivatedDigest != digestOf(t, edited) {
		t.Fatalf("setup: blocked = %+v", blocked)
	}
	if err := fixture.client.Set(fixture.ctx, fixture.prefix+":schedule_timeline:"+string(edited.Identity), original, 0).Err(); err != nil {
		t.Fatal(err)
	}
	third := cutoverCatalog(t, 95, nil)
	thirdSnapshot, err := fixture.publishNoEnsure(t, third, 180)
	if err != nil {
		t.Fatal(err)
	}
	if blocked := fixture.blocked(t); len(blocked) != 0 {
		t.Fatalf("the repaired Query Group is still held back: %+v", blocked)
	}
	if cut := fixture.openSegment(t, edited.Identity, 180); cut.Start != 180 || cut.Publication.SnapshotRevision != thirdSnapshot.Publication.SnapshotRevision {
		t.Fatalf("the repaired Query Group was not cut to the new content: %+v", cut)
	}
}

// A timeline that does not decode is held back like a rewritten one: the
// rest of the publication goes ahead.
func TestAnUnreadableTimelineIsHeldBack(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-isolation-unreadable")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	_, untouched := splitEdited(t, first, second)
	fixture.publish(t, first, 60)
	if err := fixture.client.Set(fixture.ctx, fixture.prefix+":schedule_timeline:"+string(untouched.Identity), "{not a timeline", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.publishNoEnsure(t, second, 120); err != nil {
		t.Fatalf("an unreadable timeline failed the whole publication: %v", err)
	}
	blocked := fixture.blocked(t)
	if len(blocked) != 1 || blocked[0].QueryGroup != untouched.Identity || blocked[0].Reason != controlplane.CutoverReasonTimelineMissing ||
		!strings.Contains(blocked[0].Detail, "unreadable") || blocked[0].OpenDigest != "" {
		t.Fatalf("blocked = %+v, want the unreadable timeline held back with nothing to run", blocked)
	}
}

// A held reactivation keeps its publication, and with it the set of Query
// Groups a cutover held back from that publication and the body's count of
// them: deleting or rewriting either there would pass a held-back Query Group
// off as unchanged at the next cutover.
func TestAHeldReactivationKeepsTheHeldBackSet(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	prefix := "alarmd:control:cutover-isolation-reactivation"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	both := twoQueryGroupCatalog(t)
	returning, staying := both.QueryGroups[0], both.QueryGroups[1]
	progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{
		returning.Identity: {Status: execution.ProgressMissing}, staying.Identity: {Status: execution.ProgressMissing},
	}}
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(repository, compiler, semantics, progress,
		func() time.Time { return time.Unix(90, 0) })
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := repository.PublishCatalog(ctx, both)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := controlplane.NewInitialScheduleActivator(repository, compiler, semantics, func() time.Time { return time.Unix(60, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initial.Ensure(ctx, first.Publication); err != nil {
		t.Fatal(err)
	}
	onlyStaying := controlplane.Catalog{QueryGroups: []controlplane.QueryGroup{staying}}
	onlyStaying.SnapshotRevision = execution.SnapshotRevision(mustDigest(t, "alarmd-strategy-snapshot-v1", onlyStaying.QueryGroups))
	second, _, err := repository.PublishCatalog(ctx, onlyStaying)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(ctx, second.Publication); err != nil {
		t.Fatal(err)
	}
	progress.byGroup[returning.Identity] = execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: returning.Identity}, NextSlot: 70, LastFullSlot: 60,
		LastCompletionKind: execution.CompletionFull,
	}}
	third, _, err := repository.PublishCatalog(ctx, both)
	if err != nil {
		t.Fatal(err)
	}
	held, err := reconciler.Ensure(ctx, third.Publication)
	if err != nil || len(held.Draining) != 1 {
		t.Fatalf("setup: held = %+v, %v", held.Draining, err)
	}

	// A held-back set the body accounts for, as a cutover would have left it.
	set := []controlplane.BlockedQueryGroup{{QueryGroup: staying.Identity, Reason: controlplane.CutoverReasonOpenDigestMismatch, Since: 90}}
	payload, err := controlplane.EncodeBlockedSetForTest(set)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, prefix+":activation_blocked", payload, 0).Err(); err != nil {
		t.Fatal(err)
	}
	raw, err := client.Get(ctx, prefix+":activation").Bytes()
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	body["blocked_count"], body["blocked_digest"] = 1, controlplane.BlockedDigestForTest(set)
	rewritten, _ := json.Marshal(body)
	if err := client.Set(ctx, prefix+":activation", rewritten, 0).Err(); err != nil {
		t.Fatal(err)
	}
	repository.ForgetActivationCacheForTest()

	// Drained: the held Query Group comes back through the held reactivation.
	progress.byGroup[returning.Identity] = execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: returning.Identity}, NextSlot: held.Draining[0].RetiredBoundary + 60,
		LastFullSlot: held.Draining[0].RetiredBoundary, LastCompletionKind: execution.CompletionFull,
	}}
	later, err := controlplane.NewScheduleActivationReconcilerWithProgress(repository, compiler, semantics, progress,
		func() time.Time { return time.Unix(int64(held.Draining[0].RetiredBoundary)+300, 0) })
	if err != nil {
		t.Fatal(err)
	}
	reactivated, err := later.Ensure(ctx, third.Publication)
	if err != nil || len(reactivated.Draining) != 0 {
		t.Fatalf("the held Query Group was not reactivated: %+v, %v", reactivated.Draining, err)
	}
	if got, err := client.Get(ctx, prefix+":activation_blocked").Bytes(); err != nil || string(got) != string(payload) {
		t.Fatalf("the held reactivation changed the held-back set: %q, %v", got, err)
	}
	if reactivated.BlockedCount != 1 || reactivated.BlockedDigest != controlplane.BlockedDigestForTest(set) {
		t.Fatalf("the held reactivation dropped the body's count: %d %q", reactivated.BlockedCount, reactivated.BlockedDigest)
	}
}

// The ref upgrade writes through the cutover script too, with no timeline
// and the held-back set left as it is. It used to pass the layout of the
// script before the set existed, which wrote the header, the body and the
// delta and then failed on the missing argument - a write that happened and
// reported an error.
func TestTheActivationRefUpgradeWritesWholeAndKeepsTheHeldBackSet(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-isolation-ref-upgrade")
	fixture.publish(t, cutoverCatalog(t, 80, nil), 60)
	previous := fixture.activation(t)
	if err := fixture.client.Set(fixture.ctx, fixture.prefix+":activation_blocked", "kept as it is", 0).Err(); err != nil {
		t.Fatal(err)
	}
	active, err := fixture.client.Get(fixture.ctx, fixture.prefix+":active_qg_set:"+previous.ActiveQGSetRef.Digest).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	next := previous
	next.RecordRevision++
	expected := controlplane.ActivationExpectation{RecordRevision: previous.RecordRevision, Current: previous.Current, Pending: previous.Pending}
	// The script is handed the set's length, not the set (N15).
	if err := fixture.repository.PersistActivationRefUpgradeForTest(fixture.ctx, expected, next, []byte(strconv.Itoa(len(active)))); err != nil {
		t.Fatalf("the ref upgrade failed: %v", err)
	}
	fixture.repository.ForgetActivationCacheForTest()
	if after := fixture.activation(t); after.RecordRevision != previous.RecordRevision+1 {
		t.Fatalf("record revision = %d, want %d", after.RecordRevision, previous.RecordRevision+1)
	}
	if got, err := fixture.client.Get(fixture.ctx, fixture.prefix+":activation_blocked").Result(); err != nil || got != "kept as it is" {
		t.Fatalf("the ref upgrade changed the held-back set: %q, %v", got, err)
	}
}

// A caller that hands the cutover script the layout of an older version of
// it is refused before anything is written.
func TestTheCutoverScriptRefusesAnOldArgumentLayoutWhole(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-isolation-old-layout")
	fixture.publish(t, cutoverCatalog(t, 80, nil), 60)
	header, err := fixture.client.Get(fixture.ctx, fixture.prefix+":activation_header").Result()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := fixture.client.Get(fixture.ctx, fixture.prefix+":activation").Result()
	state := fixture.activation(t)
	err = fixture.client.Eval(fixture.ctx, controlplane.CutoverScriptForTest(),
		[]string{fixture.prefix + ":activation_header", fixture.prefix + ":activation",
			fixture.prefix + ":active_qg_set:" + state.ActiveQGSetRef.Digest, fixture.prefix + ":activation_delta:999"},
		header, "rewritten-header", "rewritten-body", "", 1000, "delta", 0).Err()
	if err == nil || !strings.Contains(err.Error(), "argument layout") {
		t.Fatalf("err = %v, want the layout refused", err)
	}
	if got, _ := fixture.client.Get(fixture.ctx, fixture.prefix+":activation_header").Result(); got != header {
		t.Fatal("the header was written before the refusal")
	}
	if got, _ := fixture.client.Get(fixture.ctx, fixture.prefix+":activation").Result(); got != body {
		t.Fatal("the body was written before the refusal")
	}
	// An Assignment record key without its revision: refused whole too, not
	// after the header and body are written.
	err = fixture.client.Eval(fixture.ctx, controlplane.CutoverScriptForTest(),
		[]string{fixture.prefix + ":activation_header", fixture.prefix + ":activation",
			fixture.prefix + ":active_qg_set:" + state.ActiveQGSetRef.Digest, fixture.prefix + ":activation_delta:999",
			fixture.prefix + ":activation_blocked", fixture.prefix + ":assignment:some-query-group"},
		header, "rewritten-header", "rewritten-body", "", 1000, "delta", 0, "=").Err()
	if err == nil || !strings.Contains(err.Error(), "argument layout") {
		t.Fatalf("err = %v, want a record key without its revision refused", err)
	}
	if got, _ := fixture.client.Get(fixture.ctx, fixture.prefix+":activation_header").Result(); got != header {
		t.Fatal("the header was written before the refusal of a record without its revision")
	}
}

// movingCatalog is two strategies of one business: in the first
// publication they share query facts and one Query Group; in the second the
// second strategy queries another table and moves to a Query Group of its
// own.
func movingCatalog(t *testing.T, moved bool) controlplane.Catalog {
	t.Helper()
	documents := realThresholdDocuments(t)
	var second map[string]any
	if err := json.Unmarshal(documents[0], &second); err != nil {
		t.Fatal(err)
	}
	second["id"] = float64(1003)
	for _, item := range second["items"].([]any) {
		item.(map[string]any)["id"] = float64(13)
	}
	secondDocument, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	planner := queryPlannerFunc(func(_ context.Context, source controlplane.PrimaryQuerySource) (execution.QueryPlanFacts, error) {
		facts := queryFactsFor(t, "2", "bkcc__2")
		if moved && source.StrategyID == "1003" {
			facts.QueryRevision = ""
			facts.QueryList = append([]execution.QueryClause(nil), facts.QueryList...)
			facts.QueryList[0].TableID = "system.mem"
			return execution.BuildQueryPlanFacts(facts)
		}
		return facts, nil
	})
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{
		{SourceID: "1001", Document: documents[0], Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}},
		{SourceID: "1003", Document: secondDocument, Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}},
	}, Planner: planner})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

// A Plan that moved to another Query Group in the publication that held its
// old Query Group back is activated once, under the Query Group that has it
// now: the held-back one's carried record for it gives way. Kept, the Plan
// would be named twice.
func TestAPlanThatMovedAwayFromAHeldBackQueryGroupIsActivatedOnce(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-isolation-moved")
	first, second := movingCatalog(t, false), movingCatalog(t, true)
	groupOf := func(catalog controlplane.Catalog, strategy string) controlplane.QueryGroup {
		for _, group := range catalog.QueryGroups {
			for _, plan := range group.Plans {
				if plan.Identity.StrategyID == strategy {
					return group
				}
			}
		}
		t.Fatalf("strategy %s is in no Query Group; dispositions %+v", strategy, catalog.Dispositions)
		return controlplane.QueryGroup{}
	}
	from, to := groupOf(first, "1003"), groupOf(second, "1003")
	if from.Identity == to.Identity || groupOf(first, "1001").Identity != from.Identity || groupOf(second, "1001").Identity != from.Identity {
		t.Fatalf("setup: 1003 does not move out of 1001's Query Group (%s -> %s)", from.Identity, to.Identity)
	}
	fixture.publish(t, first, 60)
	fixture.rewriteOpenDigest(t, from.Identity, digestOf(t, to))
	if _, err := fixture.publishNoEnsure(t, second, 120); err != nil {
		t.Fatalf("a Plan that moved away from a held-back Query Group failed the publication: %v", err)
	}
	if blocked := fixture.blocked(t); len(blocked) != 1 || blocked[0].QueryGroup != from.Identity {
		t.Fatalf("blocked = %+v, want the Query Group 1003 left", blocked)
	}
	count := 0
	var named controlplane.PlanActivationRecord
	for _, record := range fixture.activation(t).Plans {
		if record.Fact.Plan.StrategyID == "1003" {
			count++
			named = record
		}
	}
	if count != 1 {
		t.Fatalf("1003 is named %d times in the activation, want once", count)
	}
	opened := fixture.openSegment(t, to.Identity, 120)
	if named.Publication.SnapshotRevision != opened.Publication.SnapshotRevision {
		t.Fatalf("1003's record names %+v, want the Query Group that has it now (%+v)", named.Publication, opened.Publication)
	}
}
