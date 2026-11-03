// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"context"
	"testing"
	"time"
)

// A content change under a live lease caps the holder's renewal at the
// change's effective time, so a holder still on the old content stops
// writing it there. A holder that already runs the new content - its Segment
// cut over before the change fell due, the common case - is not who the cap
// protects against: it renews uncapped, keeps its lease across the switch
// and is on the promoted scope after it, with no lapse and no re-acquire.
func TestAHolderAlreadyOnThePendingContentKeepsItsLeaseAcrossTheSwitch(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first := publishScope(t, store, authority, 0, "worker-1", "view-a", now)
	lease, err := store.Acquire(ctx, "query-group-1", "worker-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	effective := serverDeadline(t, store, "query-group-1").Add(ContentSwitchMargin)
	publishScope(t, store, authority, first.RecordRevision, "worker-1", "view-b", now.Add(time.Second))

	// Still on the old content, or saying nothing: capped, as before.
	for _, declared := range []string{"view-a", ""} {
		if _, err := store.RenewDeclaring(ctx, lease.Fence, declared, now.Add(2*time.Second), 2*time.Minute); err != nil {
			t.Fatalf("RenewDeclaring(%q) = %v", declared, err)
		}
		if minted := serverDeadline(t, store, "query-group-1"); !minted.Equal(effective) {
			t.Fatalf("declared %q: server deadline = %v, want capped at the effective time %v", declared, minted, effective)
		}
	}
	// On the new content already: not capped.
	renewed, err := store.RenewDeclaring(ctx, lease.Fence, "view-b", now.Add(3*time.Second), 2*time.Minute)
	if err != nil {
		t.Fatalf("RenewDeclaring(view-b) = %v", err)
	}
	if minted := serverDeadline(t, store, "query-group-1"); !minted.After(effective) {
		t.Fatalf("declared the pending scope: server deadline = %v, want the two minutes asked, past the effective time %v", minted, effective)
	}
	if renewed.PendingContentScope != "view-b" || !renewed.ContentChangePending() {
		t.Fatalf("renewal = %+v, want the pending change still named", renewed)
	}
	// Past the effective time the same lease holds, on the promoted scope.
	elapseOnRedis(t, store, "query-group-1", time.Minute+ContentSwitchMargin+time.Second)
	if err := store.CheckFenceForContentScope(ctx, lease.Fence, "view-b"); err != nil {
		t.Fatalf("CheckFenceForContentScope(view-b) after the switch = %v, want the same lease valid", err)
	}
	after, err := store.RenewDeclaring(ctx, lease.Fence, "view-b", now.Add(70*time.Second), time.Minute)
	if err != nil || after.ContentScope != "view-b" || after.ContentChangePending() || after.Fence.OwnerEpoch != lease.Fence.OwnerEpoch {
		t.Fatalf("renewal after the switch = (%+v, %v), want the same epoch on view-b with nothing pending", after, err)
	}
}
