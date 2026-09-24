// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// sixtyHours is what a Plan evaluated every sixty hours asks its content to
// be kept for once a newer publication supersedes it.
const sixtyHours = 60*time.Hour + 13*time.Minute

// activateObjectCatalog publishes the catalog and cuts it over, so the
// renewal has a current activation to renew.
func activateObjectCatalog(t *testing.T, harness *objectCatalogHarness, catalog controlplane.Catalog) controlplane.CatalogManifest {
	t.Helper()
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
	return manifest
}

func (harness *objectCatalogHarness) pttl(t *testing.T, key string) time.Duration {
	t.Helper()
	ttl, err := harness.client.PTTL(harness.ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	return ttl
}

// A long Plan's content outlives the rest of the catalog: its objects and
// output contexts are written and renewed for as long as the publication asks,
// and the manifest for the catalog TTL. Stretching the manifest too is what
// would have cost a quarter of a gigabyte on a live deployment; its frozen
// Slots read content by digest and do not need it.
func TestALongPlansContentOutlivesTheManifest(t *testing.T) {
	harness := newObjectCatalogHarness(t)
	catalog := objectCatalogTwoGroups(t, 80)
	catalog.ObjectRetention = sixtyHours
	manifest := activateObjectCatalog(t, harness, catalog)
	object := harness.prefix + ":qgobj:" + string(manifest.QueryGroups[0].ObjectDigest)
	outctx := harness.prefix + ":outctx:" + string(manifest.Plans[0].ContextDigest)
	manifestKey := harness.prefix + ":manifest:" + string(catalog.SnapshotRevision)

	if ttl := harness.pttl(t, object); ttl <= time.Hour || ttl > sixtyHours {
		t.Fatalf("object written to live %s, want the publication's %s, beyond the one hour catalog TTL", ttl, sixtyHours)
	}
	if ttl := harness.pttl(t, manifestKey); ttl > time.Hour {
		t.Fatalf("manifest written to live %s, want the catalog TTL of an hour", ttl)
	}

	// Cut back as if written by an older build, then renewed: the renewal
	// restores the long life for content and keeps the manifest short.
	for _, key := range []string{object, outctx} {
		if err := harness.client.PExpire(harness.ctx, key, time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := harness.repository.RenewCurrentActivationObjects(harness.ctx); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{object, outctx} {
		if ttl := harness.pttl(t, key); ttl <= time.Hour {
			t.Fatalf("%s renewed to %s, want the publication's %s", key, ttl, sixtyHours)
		}
	}
	if ttl := harness.pttl(t, manifestKey); ttl > time.Hour {
		t.Fatalf("manifest renewed to %s, want the catalog TTL", ttl)
	}
	if facts := harness.observer.last(t, "renew"); facts.Present != 5 || facts.Missing != 0 {
		t.Fatalf("renew facts = %+v, want the five catalog keys counted and the retention beside them not", facts)
	}
}

// A leader that has just started renews before it has admitted anything, so
// the retention cannot come from its memory: it is read from beside the
// manifest. A renewal that used the catalog TTL here would cut a long Plan's
// objects back on the first round after every restart.
func TestAFreshLeadersFirstRenewalKeepsALongPlansContent(t *testing.T) {
	harness := newObjectCatalogHarness(t)
	catalog := objectCatalogTwoGroups(t, 80)
	catalog.ObjectRetention = sixtyHours
	manifest := activateObjectCatalog(t, harness, catalog)
	object := harness.prefix + ":qgobj:" + string(manifest.QueryGroups[0].ObjectDigest)
	if err := harness.client.PExpire(harness.ctx, object, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}

	fresh := harness.newRepository(t)
	if err := fresh.RenewCurrentActivationObjects(harness.ctx); err != nil {
		t.Fatal(err)
	}
	if ttl := harness.pttl(t, object); ttl <= time.Hour {
		t.Fatalf("a fresh leader's first renewal left the object %s to live, want the publication's %s", ttl, sixtyHours)
	}
}

// A publication with no long Plan keeps today's lives exactly, and leaves no
// retention behind it for the renewal to find.
func TestAPublicationWithNoLongPlanKeepsTheCatalogTTL(t *testing.T) {
	harness := newObjectCatalogHarness(t)
	catalog := objectCatalogTwoGroups(t, 80)
	manifest := activateObjectCatalog(t, harness, catalog)
	object := harness.prefix + ":qgobj:" + string(manifest.QueryGroups[0].ObjectDigest)
	if ttl := harness.pttl(t, object); ttl > time.Hour {
		t.Fatalf("object lives %s with no long Plan, want the catalog TTL", ttl)
	}
	if exists, err := harness.client.Exists(harness.ctx, harness.prefix+":object_retention:"+string(catalog.SnapshotRevision)).Result(); err != nil || exists != 0 {
		t.Fatalf("retention key exists=(%d,%v) for a publication with no long Plan", exists, err)
	}
	if err := harness.repository.RenewCurrentActivationObjects(harness.ctx); err != nil {
		t.Fatal(err)
	}
	if ttl := harness.pttl(t, object); ttl > time.Hour {
		t.Fatalf("object renewed to %s with no long Plan, want the catalog TTL", ttl)
	}
}
