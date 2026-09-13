// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// activatedHarness publishes and activates one catalog and hands back the
// harness and the activation state.
func activatedHarness(t *testing.T) (*changeGateHarness, controlplane.ActivationState) {
	t.Helper()
	harness := newChangeGateHarness(t)
	settled := harness.settle()
	activation, err := controlplane.NewScheduleActivationReconciler(harness.repository, harness.compiler, harness.semantics,
		func() time.Time { return harness.clock })
	if err != nil {
		t.Fatal(err)
	}
	state, err := activation.Ensure(harness.ctx, settled.Publication)
	if err != nil {
		t.Fatal(err)
	}
	return harness, state
}

// The renewal of the current activation keeps the manifest of its
// publication alive and refuses to run without it.
func TestActivationRenewalRequiresTheManifest(t *testing.T) {
	harness, state := activatedHarness(t)
	revision := string(state.Current.SnapshotRevision)
	manifestKey := harness.prefix + ":manifest:" + revision
	if err := harness.client.PExpire(harness.ctx, manifestKey, 2*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	if err := harness.repository.RenewCurrentActivationObjects(harness.ctx); err != nil {
		t.Fatalf("RenewCurrentActivationObjects() error = %v", err)
	}
	if ttl, err := harness.client.PTTL(harness.ctx, manifestKey).Result(); err != nil || ttl < 30*time.Minute {
		t.Fatalf("manifest TTL after renewal = (%s, %v), want renewed", ttl, err)
	}
	if err := harness.client.Del(harness.ctx, manifestKey).Err(); err != nil {
		t.Fatal(err)
	}
	if err := harness.repository.RenewCurrentActivationObjects(harness.ctx); err == nil {
		t.Fatalf("renewal without the manifest succeeded for revision %s", revision)
	}
}

// A manifest that expired under a Leader that still remembers the revision
// as written is written back, with its objects, by the next unchanged
// round, and the occurrence keys are renewed only once it is there again.
func TestUnchangedRoundWritesTheManifestBackWhenItExpired(t *testing.T) {
	harness, state := activatedHarness(t)
	revision := string(state.Current.SnapshotRevision)
	manifestKey := harness.prefix + ":manifest:" + revision
	epochKey := harness.prefix + ":snapshot_epoch:" + revision
	if deleted := harness.client.Del(harness.ctx, manifestKey).Val(); deleted != 1 {
		t.Fatal("manifest key was not present to delete")
	}
	if err := harness.client.PExpire(harness.ctx, epochKey, 2*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	result, err := harness.reconciler.Refresh(harness.ctx, harness.source, harness.planner)
	if err != nil || result.Status != controlplane.SourceRefreshUnchanged || result.Publication != state.Current {
		t.Fatalf("unchanged round after the manifest expired = (%+v, %v), want unchanged under %+v", result, err, state.Current)
	}
	if exists := harness.client.Exists(harness.ctx, manifestKey).Val(); exists != 1 {
		t.Fatal("the unchanged round did not write the manifest back")
	}
	if ttl, err := harness.client.PTTL(harness.ctx, epochKey).Result(); err != nil || ttl < 30*time.Minute {
		t.Fatalf("revision epoch TTL after the round = (%s, %v), want renewed", ttl, err)
	}
	loaded, err := loadPublishedSnapshot(harness.ctx, harness.repository, state.Current)
	if err != nil || len(loaded.QueryGroups) == 0 {
		t.Fatalf("content after the manifest was written back = (%d groups, %v)", len(loaded.QueryGroups), err)
	}
}
