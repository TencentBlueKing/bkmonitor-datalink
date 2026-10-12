// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Session that has stopped accepting answers no deadline, by either way of
// stopping: released, or its renewal refused by the store. Its last renewal
// still had time left in both; that time is no longer the Session's to admit
// work against.
func TestAStoppedSessionAnswersNoDeadline(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	fence := execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"}
	for _, stop := range []struct {
		name string
		stop func(*Session, *fakeLeaseStore) error
	}{
		{"released", func(session *Session, _ *fakeLeaseStore) error { return session.Release(context.Background()) }},
		{"renewal refused", func(session *Session, store *fakeLeaseStore) error {
			store.renewErr = ErrNotDesired
			if err := session.Renew(context.Background(), now.Add(time.Second), time.Minute); err == nil {
				t.Fatal("the refused renewal returned no error")
			}
			return nil
		}},
	} {
		t.Run(stop.name, func(t *testing.T) {
			store := &fakeLeaseStore{lease: Lease{Fence: fence, Deadline: now.Add(time.Minute)}}
			session, err := OpenSession(context.Background(), store, "query-group-1", "worker-1", now, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if got := session.Deadline(); !got.Equal(now.Add(time.Minute)) {
				t.Fatalf("Deadline() while held = %v, want the lease's", got)
			}
			if err := stop.stop(session, store); err != nil {
				t.Fatal(err)
			}
			if got := session.Deadline(); !got.IsZero() {
				t.Fatalf("Deadline() after the Session stopped = %v, want none", got)
			}
		})
	}
}
