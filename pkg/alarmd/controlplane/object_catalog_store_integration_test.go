// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type objectCatalogObserver struct {
	mu    sync.Mutex
	facts []observability.ObjectCatalogFacts
}

func (observer *objectCatalogObserver) Observe(_ context.Context, observation observability.Observation) {
	if observation.ObjectCatalog == nil {
		return
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.facts = append(observer.facts, *observation.ObjectCatalog)
}

func (observer *objectCatalogObserver) count() int {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return len(observer.facts)
}

func (observer *objectCatalogObserver) last(t *testing.T, operation string) observability.ObjectCatalogFacts {
	t.Helper()
	observer.mu.Lock()
	defer observer.mu.Unlock()
	for index := len(observer.facts) - 1; index >= 0; index-- {
		if observer.facts[index].Operation == operation {
			return observer.facts[index]
		}
	}
	t.Fatalf("no %s of the object catalog was observed", operation)
	return observability.ObjectCatalogFacts{}
}

type objectCatalogHarness struct {
	ctx        context.Context
	client     *redis.Client
	prefix     string
	repository *controlplane.RedisCatalogRepository
	observer   *objectCatalogObserver
}

func newObjectCatalogHarness(t *testing.T) *objectCatalogHarness {
	t.Helper()
	harness := &objectCatalogHarness{ctx: context.Background(), client: newControlplaneRedis(t), prefix: "alarmd:control:object-catalog", observer: &objectCatalogObserver{}}
	harness.repository = harness.newRepository(t)
	return harness
}

func (harness *objectCatalogHarness) newRepository(t *testing.T) *controlplane.RedisCatalogRepository {
	t.Helper()
	repository, err := controlplane.NewRedisCatalogRepository(harness.client, harness.prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	repository.ConfigureObserver(harness.observer)
	return repository
}

func (harness *objectCatalogHarness) publish(t *testing.T, catalog controlplane.Catalog) controlplane.PublishedSnapshot {
	t.Helper()
	snapshot, _, err := harness.repository.PublishCatalog(harness.ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// objectCatalogTwoGroups builds a catalog with the fixture strategy on
// business 2 (Query Group A) and the second fixture strategy on business 3
// (Query Group B), with A's threshold set as given.
func objectCatalogTwoGroups(t *testing.T, thresholdA int) controlplane.Catalog {
	t.Helper()
	documents := realThresholdDocuments(t)
	documentA := []byte(strings.Replace(string(documents[0]), `"threshold":80`, `"threshold":`+strconv.Itoa(thresholdA), 1))
	var decoded map[string]any
	if err := json.Unmarshal(withWireIdentity(t, documents[1], "tenant-a", "bkcc__3"), &decoded); err != nil {
		t.Fatal(err)
	}
	decoded["bk_biz_id"] = float64(3)
	documentB, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	planner := &businessPlanner{facts: map[string]execution.QueryPlanFacts{"2": queryFactsFor(t, "2", "bkcc__2"), "3": queryFactsFor(t, "3", "bkcc__3")}}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{
		{SourceID: "1001", Document: documentA, Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}},
		{SourceID: "1002", Document: documentB, Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "3", SpaceScope: "bkcc__3"}},
	}, Planner: planner})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 2 {
		t.Fatalf("expected two Query Groups, got %d with dispositions %+v", len(catalog.QueryGroups), catalog.Dispositions)
	}
	return catalog
}

type businessPlanner struct {
	facts map[string]execution.QueryPlanFacts
}

func (planner *businessPlanner) CompilePrimaryQuery(_ context.Context, source controlplane.PrimaryQuerySource) (execution.QueryPlanFacts, error) {
	facts, ok := planner.facts[source.Identity.BusinessID]
	if !ok {
		return execution.QueryPlanFacts{}, errors.New("no query facts for business")
	}
	return facts, nil
}

// TestPublicationWritesContentAddressedObjectsAndManifest checks what one
// publication leaves in Redis: a manifest naming every Query Group's object
// and every Plan's output context, each stored under its digest with the
// Catalog TTL, each hashing to the digest that names it, and each decoding
// into the object it claims to be.
func TestPublicationWritesContentAddressedObjectsAndManifest(t *testing.T) {
	harness := newObjectCatalogHarness(t)
	catalog := objectCatalogTwoGroups(t, 80)
	harness.publish(t, catalog)

	manifest, err := harness.repository.LoadCatalogManifest(harness.ctx, catalog.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SnapshotRevision != catalog.SnapshotRevision || len(manifest.QueryGroups) != 2 || len(manifest.Plans) != 2 {
		t.Fatalf("manifest=%+v", manifest)
	}
	for _, group := range catalog.QueryGroups {
		want, err := controlplane.DeriveQueryGroupObjectDigest(group)
		if err != nil {
			t.Fatal(err)
		}
		var named execution.ObjectDigest
		for _, entry := range manifest.QueryGroups {
			if entry.QueryGroup == group.Identity {
				named = entry.ObjectDigest
			}
		}
		if named != want {
			t.Fatalf("manifest names %s for %s, want %s", named, group.Identity, want)
		}
		payload, err := harness.client.Get(harness.ctx, harness.prefix+":qgobj:"+string(want)).Bytes()
		if err != nil {
			t.Fatalf("object %s: %v", want, err)
		}
		hashed, err := contract.DeriveCanonicalDigestV2OverCanonical("alarmd-query-group-object-v1", payload)
		if err != nil || execution.ObjectDigest(hashed) != want {
			t.Fatalf("stored object hashes to %s, want %s (%v)", hashed, want, err)
		}
		var object controlplane.QueryGroupObject
		if err := json.Unmarshal(payload, &object); err != nil || object.Identity != group.Identity || len(object.Plans) != len(group.Plans) {
			t.Fatalf("stored object=%+v err=%v", object, err)
		}
		if ttl, err := harness.client.PTTL(harness.ctx, harness.prefix+":qgobj:"+string(want)).Result(); err != nil || ttl <= 0 || ttl > time.Hour {
			t.Fatalf("object TTL=(%s,%v)", ttl, err)
		}
		for _, plan := range group.Plans {
			wantContext, err := controlplane.DeriveOutputContextDigest(plan)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := harness.client.Get(harness.ctx, harness.prefix+":outctx:"+string(wantContext)).Bytes()
			if err != nil {
				t.Fatalf("output context %s: %v", wantContext, err)
			}
			hashed, err := contract.DeriveCanonicalDigestV2OverCanonical("alarmd-output-context-v1", payload)
			if err != nil || execution.OutputContextDigest(hashed) != wantContext {
				t.Fatalf("stored output context hashes to %s, want %s (%v)", hashed, wantContext, err)
			}
		}
	}
	if ttl, err := harness.client.PTTL(harness.ctx, harness.prefix+":manifest:"+string(catalog.SnapshotRevision)).Result(); err != nil || ttl <= 0 || ttl > time.Hour {
		t.Fatalf("manifest TTL=(%s,%v)", ttl, err)
	}
	facts := harness.observer.last(t, "write")
	if facts.Result != "success" || facts.Written != 4 || facts.Present != 0 || facts.QueryGroups != 2 || facts.ManifestBytes == 0 || facts.ObjectBytes == 0 {
		t.Fatalf("write facts=%+v", facts)
	}
}

// TestPublicationWritesOnlyTheObjectsThatChanged is the delta contract: an
// edit that changes one Query Group's execution content writes that Query
// Group's object and its Plan's output context and nothing else; an edit
// that changes only rendering context writes one output context and no
// object; a republication of the same revision writes nothing and does not
// even ask Redis.
func TestPublicationWritesOnlyTheObjectsThatChanged(t *testing.T) {
	harness := newObjectCatalogHarness(t)
	first := objectCatalogTwoGroups(t, 80)
	harness.publish(t, first)
	observed := harness.observer.count()

	harness.publish(t, first)
	if harness.observer.count() != observed {
		t.Fatal("republishing the same revision touched the object catalog")
	}

	second := objectCatalogTwoGroups(t, 90)
	harness.publish(t, second)
	facts := harness.observer.last(t, "write")
	if facts.Result != "success" || facts.Written != 2 || facts.Present != 2 {
		t.Fatalf("threshold edit facts=%+v", facts)
	}
	manifest, err := harness.repository.LoadCatalogManifest(harness.ctx, second.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := harness.repository.LoadCatalogManifest(harness.ctx, first.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	changed := 0
	for _, entry := range manifest.QueryGroups {
		for _, before := range previous.QueryGroups {
			if before.QueryGroup == entry.QueryGroup && before.ObjectDigest != entry.ObjectDigest {
				changed++
			}
		}
	}
	if changed != 1 {
		t.Fatalf("%d Query Group objects changed on a one-strategy edit: before=%+v after=%+v", changed, previous.QueryGroups, manifest.QueryGroups)
	}

	// A rename changes only the source document the compatibility context
	// carries; the test edits the published Plan the way the compiler would
	// have and recomputes the revision.
	renamed := objectCatalogTwoGroups(t, 90)
	renamed.QueryGroups[0].Plans[0].Plan.LegacyOutput.Strategy = append(json.RawMessage(nil), []byte(strings.Replace(string(renamed.QueryGroups[0].Plans[0].Plan.LegacyOutput.Strategy), `"id":`, `"name":"renamed","id":`, 1))...)
	revision, err := deriveRevisionForTest(renamed)
	if err != nil {
		t.Fatal(err)
	}
	renamed.SnapshotRevision = revision
	harness.publish(t, renamed)
	facts = harness.observer.last(t, "write")
	if facts.Result != "success" || facts.Written != 1 || facts.Present != 3 {
		t.Fatalf("rename facts=%+v", facts)
	}
}

// deriveRevisionForTest recomputes a catalog's snapshot revision after a test
// edited a published Plan in memory, the way the compiler would have.
func deriveRevisionForTest(catalog controlplane.Catalog) (execution.SnapshotRevision, error) {
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-strategy-snapshot-v1", catalog.QueryGroups)
	return execution.SnapshotRevision(digest), err
}

// TestRenewalKeepsTheCurrentCatalogAliveAndNoticesMissingObjects checks the
// renewal path: every object the current manifest names gets its TTL back,
// an object that went missing is counted and rewritten by the next
// publication of that revision, and a fresh process finds what an earlier
// one wrote instead of writing it again.
func TestRenewalKeepsTheCurrentCatalogAliveAndNoticesMissingObjects(t *testing.T) {
	harness := newObjectCatalogHarness(t)
	catalog := objectCatalogTwoGroups(t, 80)
	snapshot := harness.publish(t, catalog)
	progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{}}
	compiler, semantics := runtimePlanCompiler(t)
	at := time.Unix(60, 0)
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(harness.repository, compiler, semantics, progress, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(harness.ctx, snapshot.Publication); err != nil {
		t.Fatal(err)
	}
	manifest, err := harness.repository.LoadCatalogManifest(harness.ctx, catalog.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	objectKey := harness.prefix + ":qgobj:" + string(manifest.QueryGroups[0].ObjectDigest)
	if err := harness.client.PExpire(harness.ctx, objectKey, 2*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	if err := harness.repository.RenewCurrentActivationObjects(harness.ctx); err != nil {
		t.Fatal(err)
	}
	if ttl, err := harness.client.PTTL(harness.ctx, objectKey).Result(); err != nil || ttl <= 2*time.Second {
		t.Fatalf("object TTL after renewal=(%s,%v)", ttl, err)
	}
	facts := harness.observer.last(t, "renew")
	if facts.Result != "success" || facts.Present != 5 || facts.Missing != 0 {
		t.Fatalf("renew facts=%+v", facts)
	}

	// A fresh process renews from the stored manifest and writes nothing.
	fresh := harness.newRepository(t)
	if err := fresh.RenewCurrentActivationObjects(harness.ctx); err != nil {
		t.Fatal(err)
	}
	if facts := harness.observer.last(t, "renew"); facts.Result != "success" || facts.Present != 5 {
		t.Fatalf("fresh renew facts=%+v", facts)
	}
	// Having renewed, the fresh process knows the objects are stored and a
	// publication of the same revision does not touch the catalog at all.
	observed := harness.observer.count()
	if _, _, err := fresh.PublishCatalog(harness.ctx, catalog); err != nil {
		t.Fatal(err)
	}
	if harness.observer.count() != observed {
		t.Fatalf("a publication of a revision proven present touched the object catalog: %+v", harness.observer.last(t, "write"))
	}
	// A process that has not renewed asks Redis which objects exist and
	// writes none of the stored ones.
	unaware := harness.newRepository(t)
	if _, _, err := unaware.PublishCatalog(harness.ctx, catalog); err != nil {
		t.Fatal(err)
	}
	if facts := harness.observer.last(t, "write"); facts.Result != "success" || facts.Written != 0 || facts.Present != 4 {
		t.Fatalf("a fresh process rewrote objects that were already stored: %+v", facts)
	}

	// An object that went missing is noticed by the renewal and written back
	// by the next publication of the same revision.
	if err := harness.client.Del(harness.ctx, objectKey).Err(); err != nil {
		t.Fatal(err)
	}
	if err := harness.repository.RenewCurrentActivationObjects(harness.ctx); err != nil {
		t.Fatal(err)
	}
	if facts := harness.observer.last(t, "renew"); facts.Missing != 1 || facts.Present != 4 {
		t.Fatalf("renew facts after deletion=%+v", facts)
	}
	harness.publish(t, catalog)
	if facts := harness.observer.last(t, "write"); facts.Written != 1 || facts.Present != 3 {
		t.Fatalf("write facts after deletion=%+v", facts)
	}
	if exists, err := harness.client.Exists(harness.ctx, objectKey).Result(); err != nil || exists != 1 {
		t.Fatalf("deleted object was not written back: exists=%d err=%v", exists, err)
	}
}

// TestManifestCollisionIsRefusedAndDoesNotFailThePublication: a manifest
// stored under a revision with other bytes is left alone, the write is
// observed as a failure, and the publication of the whole Snapshot proceeds.
func TestManifestCollisionIsRefusedAndDoesNotFailThePublication(t *testing.T) {
	harness := newObjectCatalogHarness(t)
	catalog := objectCatalogTwoGroups(t, 80)
	manifestKey := harness.prefix + ":manifest:" + string(catalog.SnapshotRevision)
	if err := harness.client.Set(harness.ctx, manifestKey, `{"schema_version":"other"}`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	snapshot, created, err := harness.repository.PublishCatalog(harness.ctx, catalog)
	if err != nil || !created || snapshot.Publication.SnapshotRevision != catalog.SnapshotRevision {
		t.Fatalf("publication=(%+v,%t,%v)", snapshot, created, err)
	}
	if facts := harness.observer.last(t, "write"); facts.Result != "failure" {
		t.Fatalf("write facts=%+v", facts)
	}
	if stored, err := harness.client.Get(harness.ctx, manifestKey).Result(); err != nil || stored != `{"schema_version":"other"}` {
		t.Fatalf("colliding manifest was overwritten: %q %v", stored, err)
	}
	if _, err := harness.repository.LoadCatalogManifest(harness.ctx, catalog.SnapshotRevision); err == nil {
		t.Fatal("a manifest with another schema must not decode as this revision's")
	}
}
