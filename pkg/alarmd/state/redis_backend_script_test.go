// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestNoScriptReplyMatchesOnlyAnUncachedScript pins the one reply that lets a
// write be sent again. Any other error leaves the effect of the write unknown,
// and retrying it could apply it twice.
func TestNoScriptReplyMatchesOnlyAnUncachedScript(t *testing.T) {
	if !noScriptReply(errors.New("NOSCRIPT No matching script. Please use EVAL.")) {
		t.Fatal("an uncached script was not recognised")
	}
	for _, other := range []error{
		nil,
		errors.New("READONLY You can't write against a read only replica."),
		errors.New("LOADING Redis is loading the dataset in memory"),
		errors.New("ERR value is not an integer or out of range"),
		errors.New("connection reset by peer"),
		errors.New("script NOSCRIPT mentioned late"),
	} {
		if noScriptReply(other) {
			t.Fatalf("error %v was taken for an uncached script", other)
		}
	}
}

// TestFencedBatchAppliesAfterTheScriptCacheIsFlushed proves the batched write
// path does not depend on the server still holding the script: a flush between
// two batches must not lose a write.
func TestFencedBatchAppliesAfterTheScriptCacheIsFlushed(t *testing.T) {
	fixture := newRedisBatchFixture(t)
	ctx := context.Background()
	guard := &FenceGuard{Keys: fixture.owners.FenceKeys(frozenRef().Slot.QueryGroup),
		OwnerID: fixture.fence.OwnerID, OwnerEpoch: fixture.fence.OwnerEpoch,
		LeaseToken: fixture.fence.LeaseToken, NowMillis: fixture.leased.Add(time.Second).UnixMilli()}

	first := []FencedWrite{{Key: "script-flush:a", ExpectedMissing: true, Value: []byte(`{"first":true}`), TTL: time.Minute}}
	outcomes, err := fixture.backend.CompareAndSetManyByDigest(ctx, guard, first)
	if err != nil {
		t.Fatalf("first batch: %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].Status != FencedWriteApplied {
		t.Fatalf("first batch outcomes = %+v", outcomes)
	}

	if err := fixture.client.ScriptFlush(ctx).Err(); err != nil {
		t.Fatalf("flush script cache: %v", err)
	}

	second := []FencedWrite{{Key: "script-flush:b", ExpectedMissing: true, Value: []byte(`{"second":true}`), TTL: time.Minute}}
	outcomes, err = fixture.backend.CompareAndSetManyByDigest(ctx, guard, second)
	if err != nil {
		t.Fatalf("batch after flush: %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].Status != FencedWriteApplied {
		t.Fatalf("outcomes after flush = %+v", outcomes)
	}
	stored, err := fixture.backend.MGet(ctx, []string{"script-flush:a", "script-flush:b"})
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(stored) != 2 || string(stored[0]) != `{"first":true}` || string(stored[1]) != `{"second":true}` {
		t.Fatalf("stored values = %q", stored)
	}
}
