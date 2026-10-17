// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycleprocess

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"linkd/internal/eventsource"
	"linkd/internal/lifecycle"
	"linkd/internal/store"
)

type batchSourceReader struct {
	calls   atomic.Int32
	version atomic.Int64
	failed  atomic.Bool
}

func (r *batchSourceReader) Get(_ context.Context, id string) (eventsource.Record, error) {
	r.calls.Add(1)
	if r.failed.Load() {
		return eventsource.Record{}, errors.New("source unavailable")
	}
	return eventsource.Record{ID: id, Published: r.version.Load()}, nil
}

func (r *batchSourceReader) GetRelease(context.Context, string, int64) (eventsource.Release, error) {
	return eventsource.Release{}, errors.New("unexpected release lookup in record fixture")
}

func TestControlSourceReadOncePerBatchAndSeesNextPublication(t *testing.T) {
	op, repo, relation, ids, held := explicitMergeFixture(t)
	sources := &batchSourceReader{}
	sources.version.Store(1)
	op.sources = sources
	prepare := op.withProcessor
	op.withProcessor = func(ctx context.Context, identity store.StoredAlert, run func(*lifecycle.Processor) error) error {
		record, err := readControlSource(ctx, sources, identity.Alert.EventSourceID)
		if err != nil {
			return err
		}
		if record.Published != sources.version.Load() {
			t.Error("batch reused previous publication")
		}
		return prepare(ctx, identity, run)
	}
	for version := int64(1); version <= 2; version++ {
		sources.version.Store(version)
		for _, item := range op.LinkMergeMembersPrepared(t.Context(), "tenant", ids, relation) {
			if item.Err != nil {
				t.Fatal(item.Err)
			}
		}
		if sources.calls.Load() != int32(version) {
			t.Fatal("source was fetched per member", sources.calls.Load())
		}
	}
	sources.failed.Store(true)
	for _, item := range op.LinkMergeMembersPrepared(t.Context(), "tenant", ids, relation) {
		if item.Err == nil {
			t.Fatal("source failure silently used old publication")
		}
	}
	if sources.calls.Load() != 3 || repo.writes.Load() != 1 || held.Load() != 0 || len(op.slots) != 0 {
		t.Fatal("source failure repeated writes or leaked leases")
	}
}
