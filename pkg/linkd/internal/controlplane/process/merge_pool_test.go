// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplaneprocess

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
	mergeflow "linkd/internal/merge"
)

func TestMergeBatchPoolStartsOnlyAvailableWorkAndRefillsBeforeSlowBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pool := mergeflow.NewMemberPool()
	batches := []mergeflow.MemberBatch{}
	for i := range 4 {
		batches = append(batches, mergeflow.MemberBatch{Key: fmt.Sprint(i), Phase: "link", State: "pending"})
	}
	pool.Stage(batches)
	started := make(chan struct{})
	finished := make(chan string, 3)
	var active, peak atomic.Int64
	execute := func(ctx context.Context, b mergeflow.MemberBatch) (mergeflow.MemberBatch, error) {
		n := active.Add(1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		defer active.Add(-1)
		if b.Key == "0" {
			close(started)
			<-ctx.Done()
			return b, ctx.Err()
		}
		b.State = "completed"
		finished <- b.Key
		return b, nil
	}
	done := make(chan error, 1)
	go func() {
		done <- runMergeBatchPool(ctx, pool, 2, execute, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), taskCatalog(config.Config{Lifecycle: &config.LifecycleConfig{}}, 0), nil)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("pool did not join running goroutines")
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first batch not started")
	}
	for range 3 {
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Fatal("slow batch blocked available slot")
		}
	}
	if peak.Load() > 2 {
		t.Fatal("exceeded configured concurrency", peak.Load())
	}
}

func TestMergeBatchPoolDirectCompletionStartsNextStage(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pool := mergeflow.NewMemberPool()
	pool.Stage([]mergeflow.MemberBatch{{Key: "refs", Phase: "references", State: "pending"}})
	advanced := make(chan struct{})
	execute := func(_ context.Context, b mergeflow.MemberBatch) (mergeflow.MemberBatch, error) {
		b.State = "completed"
		return b, nil
	}
	complete := func(_ context.Context, b mergeflow.MemberBatch) error {
		if b.Phase == "references" {
			pool.Stage([]mergeflow.MemberBatch{{Key: "link", Phase: "link", State: "pending"}})
		} else {
			close(advanced)
		}
		return nil
	}
	done := make(chan error, 1)
	go func() {
		done <- runMergeBatchPool(ctx, pool, 1, execute, complete, slog.New(slog.NewTextHandler(io.Discard, nil)), taskCatalog(config.Config{Lifecycle: &config.LifecycleConfig{}}, 0), nil)
	}()
	select {
	case <-advanced:
	case <-time.After(750 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("next stage waited for periodic polling")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop")
	}
}
