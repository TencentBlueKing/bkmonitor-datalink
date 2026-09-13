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
// publication alive and refuses to run without it; the snapshot body, for
// as long as one is still written, is renewed beside the manifest but is
// not required.
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
	if err := harness.client.Del(harness.ctx, harness.prefix+":snapshot:"+revision).Err(); err != nil {
		t.Fatal(err)
	}
	if err := harness.repository.RenewCurrentActivationObjects(harness.ctx); err != nil {
		t.Fatalf("renewal without the snapshot body error = %v, want success", err)
	}
	if err := harness.client.Del(harness.ctx, manifestKey).Err(); err != nil {
		t.Fatal(err)
	}
	if err := harness.repository.RenewCurrentActivationObjects(harness.ctx); err == nil {
		t.Fatalf("renewal without the manifest succeeded for revision %s", revision)
	}
}
