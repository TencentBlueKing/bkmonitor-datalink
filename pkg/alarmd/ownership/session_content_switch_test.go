// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A renewal says what the holder runs: the scope its Runner last noted, and
// nothing until one was noted.
func TestARenewalCarriesTheContentScopeTheHolderRuns(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	fence := execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"}
	store := &fakeLeaseStore{lease: Lease{Fence: fence, Deadline: now.Add(time.Minute)}}
	session, err := OpenSession(context.Background(), store, "query-group-1", "worker-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Renew(context.Background(), now.Add(time.Second), time.Minute); err != nil {
		t.Fatal(err)
	}
	session.NoteContentScope("view-b")
	if err := session.Renew(context.Background(), now.Add(2*time.Second), time.Minute); err != nil {
		t.Fatal(err)
	}
	if len(store.declared) != 2 || store.declared[0] != "" || store.declared[1] != "view-b" {
		t.Fatalf("declared on the renewals = %q, want nothing, then view-b", store.declared)
	}
}

// A lease capped at a content switch and run out there is a stale fence to
// every branch, and named CONTENT_SCOPE_MOVED to a reader. A lease that ran
// out anywhere else is plain stale.
func TestALeaseThatRanOutAtAContentSwitchIsNamedSo(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	fence := execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"}
	switchAt := now.Add(65 * time.Second)
	for _, test := range []struct {
		name     string
		renewed  Lease
		switched bool
	}{
		{"capped at the switch", Lease{Fence: fence, Deadline: switchAt, ContentScope: "view-a", PendingContentScope: "view-b", EffectiveAt: switchAt}, true},
		{"uncapped, the holder already on the new content", Lease{Fence: fence, Deadline: now.Add(2 * time.Minute), ContentScope: "view-a", PendingContentScope: "view-b", EffectiveAt: switchAt}, false},
		{"no change pending", Lease{Fence: fence, Deadline: switchAt}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeLeaseStore{lease: Lease{Fence: fence, Deadline: now.Add(time.Minute)}}
			session, err := OpenSession(context.Background(), store, "query-group-1", "worker-1", now, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			store.renewed = test.renewed
			if err := session.Renew(context.Background(), now.Add(10*time.Second), time.Minute); err != nil {
				t.Fatal(err)
			}
			store.renewErr = ErrStaleFence
			past := test.renewed.Deadline.Add(time.Second)
			err = session.Renew(context.Background(), past, time.Minute)
			if !errors.Is(err, ErrStaleFence) || !IsLeaseDecision(err) {
				t.Fatalf("Renew() past the deadline = %v, want a stale fence to every branch", err)
			}
			var switched *LeaseEndedAtContentSwitch
			if errors.As(err, &switched) != test.switched {
				t.Fatalf("Renew() = %v, named at the content switch = %t, want %t", err, !test.switched, test.switched)
			}
			want := contract.ReasonOwnershipStaleFence
			if test.switched {
				want = contract.ReasonContentScopeMoved
			}
			if reason, _ := RefusalReason(err); reason != want {
				t.Fatalf("RefusalReason() = %s, want %s", reason, want)
			}
			if _, err := session.ValidateCurrent(context.Background(), past); errors.As(err, &switched) != test.switched || !errors.Is(err, ErrStaleFence) {
				t.Fatalf("ValidateCurrent() past the deadline = %v, want the same naming", err)
			}
		})
	}
}
