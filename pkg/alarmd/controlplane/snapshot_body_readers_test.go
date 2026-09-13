// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Before the Control Leader may stop writing the whole snapshot body, every
// reader that a worker or a Leader of this build runs must be known to
// either read the object catalog instead or still need the body. This test
// is that inventory, run against a real Redis: a normal publication and
// activation, then the snapshot body deleted, then every reader exercised.
// The readers listed as needing the body are the work that must happen
// before the body stops being written; each one that is converted moves to
// the other list here.
func TestReadersInventoryWithoutTheSnapshotBody(t *testing.T) {
	harness := newObjectCatalogHarness(t)
	ctx := harness.ctx
	published := harness.publish(t, catalogWithSchedule(t, validCatalog(t, 80), 60, 0))
	revision := published.Publication.SnapshotRevision
	compiler, semantics := runtimePlanCompiler(t)
	tick := int64(0)
	reconciler, err := controlplane.NewScheduleActivationReconciler(harness.repository, compiler, semantics, func() time.Time {
		tick++
		return time.Unix(180+60*tick, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := reconciler.Ensure(ctx, published.Publication)
	if err != nil {
		t.Fatalf("Ensure() with the body present error = %v", err)
	}
	manifest, err := harness.repository.LoadCatalogManifest(ctx, revision)
	if err != nil || len(manifest.QueryGroups) != 1 || len(manifest.Plans) == 0 {
		t.Fatalf("manifest with the body present = (%+v, %v)", manifest, err)
	}
	// A second publication, activated by nobody yet, for the readers that
	// activate a new publication.
	next := harness.publish(t, catalogWithSchedule(t, validCatalog(t, 81), 60, 0))
	// Positive control: both bodies existed until now.
	for _, rev := range []string{string(revision), string(next.Publication.SnapshotRevision)} {
		bodyKey := harness.prefix + ":snapshot:" + rev
		if deleted := harness.client.Del(ctx, bodyKey).Val(); deleted != 1 {
			t.Fatalf("snapshot body key %q was not present to delete (deleted=%d)", bodyKey, deleted)
		}
	}

	// Readers that answer from the object catalog: the worker's steady state.
	objectReaders := []struct {
		name string
		read func() error
	}{
		{"LoadCatalogManifest", func() error { _, err := harness.repository.LoadCatalogManifest(ctx, revision); return err }},
		{"LoadQueryGroupObject", func() error {
			for _, entry := range manifest.QueryGroups {
				if _, err := harness.repository.LoadQueryGroupObject(ctx, entry.ObjectDigest); err != nil {
					return err
				}
			}
			return nil
		}},
		{"LoadActivation", func() error { _, err := harness.repository.LoadActivation(ctx); return err }},
		{"ScheduleActivationReconciler.Ensure(current publication)", func() error {
			_, err := reconciler.Ensure(ctx, published.Publication)
			return err
		}},
		{"LoadPublishedContent+LoadContentQueryGroups", func() error {
			content, err := harness.repository.LoadPublishedContent(ctx, state.Current)
			if err != nil {
				return err
			}
			identities := make([]execution.QueryGroupIdentity, 0, len(content.Groups))
			for identity := range content.Groups {
				identities = append(identities, identity)
			}
			groups, err := harness.repository.LoadContentQueryGroups(ctx, content, identities)
			if err == nil && len(groups) != len(manifest.QueryGroups) {
				return errors.New("content did not assemble every Query Group")
			}
			return err
		}},
		{"RenewCurrentActivationObjects", func() error { return harness.repository.RenewCurrentActivationObjects(ctx) }},
		{"ScheduleActivationReconciler.Ensure(next publication)", func() error { _, err := reconciler.Ensure(ctx, next.Publication); return err }},
		{"LoadActiveQueryGroupSet", func() error {
			if state.ActiveQGSetRef.Digest == "" {
				return errors.New("initial activation wrote no active Query Group set reference")
			}
			groups, err := harness.repository.LoadActiveQueryGroupSet(ctx, state.ActiveQGSetRef)
			if err == nil && len(groups) != 1 {
				return errors.New("active set does not hold the Query Group")
			}
			return err
		}},
	}
	for _, reader := range objectReaders {
		if err := reader.read(); err != nil {
			failure, typed := controlplane.ActivationFailureFromError(err)
			t.Errorf("%s needs the snapshot body: %v (activation failure %#v typed=%v)", reader.name, err, failure, typed)
		} else {
			t.Logf("%s answers without the snapshot body", reader.name)
		}
	}

	// Readers that still load the whole body. Each is a prerequisite of
	// stopping the body write; a converted reader belongs in the list above.
	bodyReaders := []struct {
		name string
		read func() error
	}{
		{"LoadPublishedSnapshot", func() error { _, err := harness.repository.LoadPublishedSnapshot(ctx, state.Current); return err }},
		{"LoadQueryGroup", func() error {
			_, err := harness.repository.LoadQueryGroup(ctx, revision, manifest.QueryGroups[0].QueryGroup)
			return err
		}},
		{"LoadPlan", func() error { _, err := harness.repository.LoadPlan(ctx, revision, manifest.Plans[0].Plan); return err }},
	}
	for _, reader := range bodyReaders {
		err := reader.read()
		switch {
		case err == nil:
			t.Errorf("%s no longer needs the snapshot body: move it to the object-catalog list", reader.name)
		case errors.Is(err, controlplane.ErrSnapshotUnavailable):
			t.Logf("%s still needs the snapshot body (prerequisite for stopping the body write)", reader.name)
		default:
			t.Errorf("%s failed without the snapshot body for another reason: %v", reader.name, err)
		}
	}
}
