// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// releasingLeaseStore grants one lease and takes its release.
type releasingLeaseStore struct {
	ownership.LeaseStore
	lease ownership.Lease
}

func (store *releasingLeaseStore) Acquire(context.Context, execution.QueryGroupIdentity, string, time.Time, time.Duration) (ownership.Lease, error) {
	return store.lease, nil
}

func (store *releasingLeaseStore) Release(context.Context, execution.OwnerFence) error { return nil }

// A lease the Session has let go admits no output, however long its last
// renewal had left: the next owner may hold the lease already, and a Slot
// still running under the released one - a hung execution resuming after its
// Query Group was declined, or one whose renewal the store refused - would
// send what the next owner sends again. While the lease is held, the same
// batch is admitted.
func TestAReleasedLeaseAdmitsNoOutput(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store := &releasingLeaseStore{lease: ownership.Lease{
		Fence:    execution.OwnerFence{QueryGroup: "qg-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "t1"},
		Deadline: now.Add(30 * time.Second),
	}}
	session, err := ownership.OpenSession(context.Background(), store, "qg-1", "worker-1", now, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	sent := 0
	producer := &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) {
		sent++
		return 0, 0, nil
	}}
	sink, err := newTriggerEventSink("alarmd-trigger-event", producer, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}
	sink.now = func() time.Time { return now.Add(5 * time.Second) }
	event := triggerEventGolden(t)
	event.WireFormat = contract.WireFormatStandardRawEvent
	ctx := execution.ContextWithLeaseAuthority(context.Background(), session)
	if err := sink.WriteBatch(ctx, []contract.TriggerEventV1{event}); err != nil || sent != 1 {
		t.Fatalf("WriteBatch() under the held lease = %v with %d sent, want it admitted", err, sent)
	}
	if err := session.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	err = sink.WriteBatch(ctx, []contract.TriggerEventV1{event})
	var deferred *OutputDeferredError
	if !errors.As(err, &deferred) || sent != 1 {
		t.Fatalf("WriteBatch() after the release = %v with %d sent in all, want it refused before the broker", err, sent)
	}
}
