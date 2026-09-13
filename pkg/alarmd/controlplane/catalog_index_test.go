// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// sharedClock advances one minute per read, so activations made by
// different reconcilers of one test still follow each other in time.
func sharedClock() func() time.Time {
	tick := int64(0)
	return func() time.Time {
		tick++
		return time.Unix(180+60*tick, 0)
	}
}

func indexReconciler(t *testing.T, repository *controlplane.RedisCatalogRepository, clock func() time.Time) *controlplane.ScheduleActivationReconciler {
	t.Helper()
	compiler, semantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, clock)
	if err != nil {
		t.Fatal(err)
	}
	return reconciler
}

// The Leader that publishes a catalog indexes it from the catalog and reads
// nothing back; a Leader that inherits a publication reads every object
// once and afterwards only the Query Groups whose digest changed. Every
// activation audits the index against the body it loaded, and the audit
// agrees on every Query Group in all three situations.
func TestCatalogIndexReadsOnlyWhatItDoesNotKnowAndAuditsAgainstTheBody(t *testing.T) {
	harness := newObjectCatalogHarness(t)
	ctx := harness.ctx
	publisher := harness.repository
	clock := sharedClock()
	first := harness.publish(t, catalogWithSchedule(t, objectCatalogTwoGroups(t, 80), 60, 0))
	if _, err := indexReconciler(t, publisher, clock).Ensure(ctx, first.Publication); err != nil {
		t.Fatalf("Ensure(first) on the publisher error = %v", err)
	}
	stats := publisher.ControlReadCacheStats()
	if stats.Index.Hits == 0 || stats.Index.Misses != 0 || stats.IndexAudit.Samples != 2 || stats.IndexAudit.Agreed != 2 ||
		stats.IndexAudit.Missed != 0 || stats.IndexAudit.OverNamed != 0 {
		t.Fatalf("publisher index stats after its own publication = %+v / %+v, want no object read and two agreed audits", stats.Index, stats.IndexAudit)
	}

	// A second process inherits the publication: cold, it reads both
	// objects once, and the audit still agrees.
	inheritor := harness.newRepository(t)
	reconciler := indexReconciler(t, inheritor, clock)
	second := harness.publish(t, catalogWithSchedule(t, objectCatalogTwoGroups(t, 81), 60, 0))
	if _, err := reconciler.Ensure(ctx, second.Publication); err != nil {
		t.Fatalf("Ensure(second) on the inheritor error = %v", err)
	}
	stats = inheritor.ControlReadCacheStats()
	if stats.Index.Misses != 2 || stats.IndexAudit.Samples != 2 || stats.IndexAudit.Agreed != 2 || stats.IndexAudit.Missed != 0 {
		t.Fatalf("inheritor index stats after a cold load = %+v / %+v, want two objects read and two agreed audits", stats.Index, stats.IndexAudit)
	}
	// A later publication changes one Query Group: one entry is reused, one
	// object is read.
	third := harness.publish(t, catalogWithSchedule(t, objectCatalogTwoGroups(t, 82), 60, 0))
	if _, err := reconciler.Ensure(ctx, third.Publication); err != nil {
		t.Fatalf("Ensure(third) on the inheritor error = %v", err)
	}
	stats = inheritor.ControlReadCacheStats()
	// The audit is sampled: the first activation of a process and every
	// sixteenth after it, so this activation adds no sample.
	if stats.Index.Misses != 3 || stats.IndexAudit.Samples != 2 || stats.IndexAudit.Agreed != 2 || stats.IndexAudit.Missed != 0 {
		t.Fatalf("inheritor index stats after a one-group change = %+v / %+v, want one more object read and no new audit sample", stats.Index, stats.IndexAudit)
	}

	// The content the index describes is the content the body carries:
	// digests and output context references per Query Group, and the
	// assembled Query Groups derive to the same object digests.
	content, err := inheritor.LoadPublishedContent(ctx, third.Publication)
	if err != nil {
		t.Fatalf("LoadPublishedContent() error = %v", err)
	}
	if len(content.Groups) != 2 {
		t.Fatalf("content names %d Query Groups, want 2", len(content.Groups))
	}
	for _, group := range third.QueryGroups {
		entry, ok := content.Groups[group.Identity]
		if !ok {
			t.Fatalf("content does not name %s", group.Identity)
		}
		digest, err := controlplane.DeriveQueryGroupObjectDigest(group)
		if err != nil {
			t.Fatal(err)
		}
		wantRefs := make([]execution.OutputContextRef, 0, len(group.Plans))
		wantPlans := make([]execution.PlanIdentity, 0, len(group.Plans))
		for _, plan := range group.Plans {
			contextDigest, err := controlplane.DeriveOutputContextDigest(plan)
			if err != nil {
				t.Fatal(err)
			}
			wantRefs = append(wantRefs, execution.OutputContextRef{Plan: plan.Identity, Digest: contextDigest})
			wantPlans = append(wantPlans, plan.Identity)
		}
		if entry.Digest != digest || !reflect.DeepEqual(entry.Plans, wantPlans) || !reflect.DeepEqual(entry.Refs, wantRefs) {
			t.Fatalf("content of %s = %+v, want digest %s plans %v refs %v", group.Identity, entry, digest, wantPlans, wantRefs)
		}
	}
	identities := make([]execution.QueryGroupIdentity, 0, 2)
	for identity := range content.Groups {
		identities = append(identities, identity)
	}
	assembled, err := inheritor.LoadContentQueryGroups(ctx, content, identities)
	if err != nil || len(assembled) != 2 {
		t.Fatalf("LoadContentQueryGroups() = %d groups, error %v", len(assembled), err)
	}
	for identity, group := range assembled {
		digest, err := controlplane.DeriveQueryGroupObjectDigest(group)
		if err != nil || digest != content.Groups[identity].Digest {
			t.Fatalf("assembled %s derives to %s (error %v), want %s", identity, digest, err, content.Groups[identity].Digest)
		}
	}
}

// Compilation is a pure function of content: two processes that activate
// the same publication independently produce byte-identical activation
// records with the same state generations. The catalog index rests on this
// when it lets an activation carry the records of Query Groups whose
// content did not change instead of compiling them again.
func TestActivationRecordsAreAPureFunctionOfPublishedContent(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	catalog := catalogWithSchedule(t, validCatalog(t, 80), 60, 0)
	var states []controlplane.ActivationState
	for _, prefix := range []string{"alarmd:control:pure-a", "alarmd:control:pure-b"} {
		repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		published, _, err := repository.PublishCatalog(ctx, catalog)
		if err != nil {
			t.Fatal(err)
		}
		compiler, semantics := runtimePlanCompiler(t)
		reconciler, err := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time { return time.Unix(240, 0) })
		if err != nil {
			t.Fatal(err)
		}
		state, err := reconciler.Ensure(ctx, published.Publication)
		if err != nil {
			t.Fatalf("Ensure() under %s error = %v", prefix, err)
		}
		states = append(states, state)
	}
	if len(states[0].Plans) == 0 || !reflect.DeepEqual(states[0].Plans, states[1].Plans) {
		t.Fatalf("activation records differ between two independent activations of the same content:\n%+v\n%+v", states[0].Plans, states[1].Plans)
	}
	for index := range states[0].Plans {
		if states[0].Plans[index].Fact.Selected.StateGeneration == "" ||
			states[0].Plans[index].Fact.Selected.StateGeneration != states[1].Plans[index].Fact.Selected.StateGeneration {
			t.Fatalf("state generation differs for %+v", states[0].Plans[index].Fact.Plan)
		}
	}
}

// Content a process remembers for a publication is served only while the
// publication stands: a manifest that is gone, or an occurrence that names
// another revision, is found on the very next read, as the body read found
// them.
func TestRememberedPublishedContentIsProbedOnEveryRead(t *testing.T) {
	harness := newObjectCatalogHarness(t)
	ctx := harness.ctx
	snapshot := harness.publish(t, catalogWithSchedule(t, objectCatalogTwoGroups(t, 80), 60, 0))
	publication := snapshot.Publication
	repository := harness.newRepository(t)
	first, err := repository.LoadPublishedContent(ctx, publication)
	if err != nil || len(first.Groups) != 2 {
		t.Fatalf("cold LoadPublishedContent = (%d groups, %v), want both Query Groups", len(first.Groups), err)
	}
	misses := repository.ControlReadCacheStats().Index.Misses
	if misses != 2 {
		t.Fatalf("cold read object misses=%d, want both objects read once", misses)
	}
	second, err := repository.LoadPublishedContent(ctx, publication)
	if err != nil || !reflect.DeepEqual(first, second) || repository.ControlReadCacheStats().Index.Misses != misses {
		t.Fatalf("second read = (%+v, %v) misses=%d, want the remembered content and no object read", second, err, repository.ControlReadCacheStats().Index.Misses)
	}

	manifestKey := harness.prefix + ":manifest:" + string(publication.SnapshotRevision)
	manifest, err := harness.client.Get(ctx, manifestKey).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.client.Del(ctx, manifestKey).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.LoadPublishedContent(ctx, publication); !errors.Is(err, controlplane.ErrSnapshotUnavailable) {
		t.Fatalf("read without the manifest error = %v, want snapshot unavailable", err)
	}
	if err := harness.client.Set(ctx, manifestKey, manifest, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.LoadPublishedContent(ctx, publication); err != nil {
		t.Fatalf("read with the manifest restored error = %v", err)
	}

	occurrenceKey := harness.prefix + ":publication:" + strconv.FormatUint(publication.PublicationEpoch, 10)
	occurrence, err := harness.client.Get(ctx, occurrenceKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.client.Set(ctx, occurrenceKey, "another-revision", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	var corrupt *controlplane.PersistedSnapshotCorruptError
	if _, err := repository.LoadPublishedContent(ctx, publication); !errors.As(err, &corrupt) {
		t.Fatalf("read under an occurrence naming another revision error = %v, want persisted corruption", err)
	}
	if err := harness.client.Set(ctx, occurrenceKey, occurrence, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.LoadPublishedContent(ctx, publication); err != nil {
		t.Fatalf("read with the occurrence restored error = %v", err)
	}
}
