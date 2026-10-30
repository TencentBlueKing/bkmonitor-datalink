// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycleprocess

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/store"
	storeassembly "linkd/internal/store/assembly"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type closeBatchCache struct {
	lifecycle.NoopRecentAlertCache
	batches, items atomic.Int64
	failID         string
}

func (c *closeBatchCache) PutCurrentSnapshots(_ context.Context, alerts []store.StoredAlert) []error {
	c.batches.Add(1)
	c.items.Add(int64(len(alerts)))
	results := make([]error, len(alerts))
	for i, a := range alerts {
		if a.Alert.AlertID == c.failID {
			results[i] = errors.New("cache unavailable")
		}
	}
	return results
}

type closeBatchProjectionStub struct {
	calls, items int
	failID       string
}

type closeBatchSources struct {
	batchSourceReader
	releases atomic.Int32
}

type closeBatchTestLocker struct{ held *atomic.Int32 }

func (l closeBatchTestLocker) AcquireBatch(_ context.Context, keys []string) ([]scheduler.Lease, []error) {
	for range keys {
		l.held.Add(1)
	}
	return make([]scheduler.Lease, len(keys)), make([]error, len(keys))
}

func (l closeBatchTestLocker) ReleaseBatch(_ context.Context, leases []scheduler.Lease) []error {
	for range leases {
		l.held.Add(-1)
	}
	return make([]error, len(leases))
}

// 内存后端的普通 GetAlerts 仍保持 500 条契约；显式关闭的逻辑批读在测试中拼装同等结果。
type closeMemoryRepository struct{ *explicitMergeRepository }

func (r *closeMemoryRepository) GetAlertsCurrent(ctx context.Context, tenant string, ids []string) (store.AlertBatch, error) {
	r.reads.Add(1)
	result := store.AlertBatch{}
	for start := 0; start < len(ids); start += store.MaxBatchSize {
		page, err := r.GetAlerts(ctx, tenant, ids[start:min(start+store.MaxBatchSize, len(ids))])
		if err != nil {
			return store.AlertBatch{}, err
		}
		result.Alerts = append(result.Alerts, page.Alerts...)
		result.NotFound = append(result.NotFound, page.NotFound...)
	}
	return result, nil
}

func (s *closeBatchSources) GetRelease(_ context.Context, id string, version int64) (eventsource.Release, error) {
	s.releases.Add(1)
	return eventsource.Release{ID: id, Version: version}, nil
}

func (*closeBatchProjectionStub) ProjectNow(context.Context, string, string) error {
	return errors.New("single projection must not be called")
}

func (p *closeBatchProjectionStub) ProjectBatchNow(_ context.Context, _ string, ids []string) []error {
	p.calls++
	p.items += len(ids)
	results := make([]error, len(ids))
	for i, id := range ids {
		if id == p.failID {
			results[i] = errors.New("projection unavailable")
		}
	}
	return results
}

func closeBatchFixture(t *testing.T, count int) (*AlertCloser, *explicitMergeRepository, *closeBatchCache, []lifecycle.CloseAlertCommand, *atomic.Int32) {
	t.Helper()
	repo := &explicitMergeRepository{Repository: memory.New()}
	cache := &closeBatchCache{}
	commands := make([]lifecycle.CloseAlertCommand, count)
	for i := range commands {
		id := fmt.Sprintf("alert-%03d", i)
		a := storetest.Alert("tenant", id, "opening", id, "warning")
		if _, err := repo.CreateAlert(t.Context(), a); err != nil {
			t.Fatal(err)
		}
		commands[i] = lifecycle.CloseAlertCommand{OperationID: "close-batch", BKTenantID: "tenant", AlertID: id, OperatorKind: domain.OperatorKindUser, OperatorID: "tester", Reason: "done", EffectiveAt: a.UpdateAt.Add(time.Hour)}
	}
	sources := &closeBatchSources{}
	sources.version.Store(1)
	held := &atomic.Int32{}
	batchRepo := &closeMemoryRepository{repo}
	closer := &AlertCloser{slots: make(chan struct{}, 4), batchSlots: make(chan struct{}, 2), batchSize: 1000, concurrency: 2, sources: sources, resources: &AlertProcessorResources{repository: &storeassembly.Runtime{Repository: batchRepo}}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	closer.batchLocker = func(string) (closeBatchLocker, error) { return closeBatchTestLocker{held}, nil }
	closer.withProcessor = func(ctx context.Context, identity store.StoredAlert, run func(*lifecycle.Processor) error) error {
		if _, err := readControlSource(ctx, sources, identity.Alert.EventSourceID); err != nil {
			return err
		}
		p, err := lifecycle.NewProcessor(batchRepo, cache, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, nil, config.DefaultSeverityConfig(), lifecycle.SystemClock{}, closer.logger)
		if err != nil {
			return err
		}
		return run(p)
	}
	return closer, repo, cache, commands, held
}

func TestCloseBatch500UsesOneCASCacheLogAndProjectionBatch(t *testing.T) {
	closer, repo, cache, commands, held := closeBatchFixture(t, 500)
	projection := &closeBatchProjectionStub{}
	closer.projection = projection
	repo.afterWrite = func() {
		if held.Load() != 500 {
			t.Errorf("CAS without all leases: %d", held.Load())
		}
	}
	results := closer.CloseAlerts(t.Context(), commands)
	for i, result := range results {
		if result.Err != nil || result.Alert.AlertID != commands[i].AlertID || result.Alert.Status != domain.AlertStatusClosed || result.AlreadyClosed {
			t.Fatalf("item %d: %+v", i, result)
		}
	}
	if repo.reads.Load() != 2 || repo.writes.Load() != 1 || repo.logs.Load() != 1 || cache.batches.Load() != 1 || cache.items.Load() != 500 || projection.calls != 1 || projection.items != 500 || closer.sources.(*closeBatchSources).calls.Load() != 1 || closer.sources.(*closeBatchSources).releases.Load() != 1 {
		t.Fatalf("not explicit bulk: reads=%d CAS=%d logs=%d cache=%d/%d projection=%d/%d", repo.reads.Load(), repo.writes.Load(), repo.logs.Load(), cache.batches.Load(), cache.items.Load(), projection.calls, projection.items)
	}
	if held.Load() != 0 || len(closer.slots) != 0 {
		t.Fatal("lease or execution slot leaked")
	}
}

func TestCloseBatch5000IsIndependentOfSingleCloseSlots(t *testing.T) {
	closer, repo, cache, commands, held := closeBatchFixture(t, 5000)
	closer.batchSize = 5000
	// 单条关闭全部占满也不影响批量入口；批内准备并发仍只有 2，不截断批大小。
	for range cap(closer.slots) {
		closer.slots <- struct{}{}
	}
	started := time.Now()
	for _, result := range closer.CloseAlerts(t.Context(), commands) {
		if result.Err != nil {
			t.Fatal(result.Err)
		}
	}
	if repo.reads.Load() != 2 || repo.writes.Load() != 1 || repo.logs.Load() != 1 || cache.batches.Load() != 1 || cache.items.Load() != 5000 || held.Load() != 0 || len(closer.batchSlots) != 0 {
		t.Fatal("large close was split by single capacity", repo.reads.Load(), repo.writes.Load(), repo.logs.Load(), cache.batches.Load())
	}
	for range cap(closer.slots) {
		<-closer.slots
	}
	t.Logf("5000 alerts, one CAS, two reads, ten 500-item log bulks: %s (memory fixture, not production throughput)", time.Since(started))
}

func TestCloseBatchPartialFailuresAndStableRetry(t *testing.T) {
	closer, repo, cache, commands, held := closeBatchFixture(t, 4)
	projection := &closeBatchProjectionStub{failID: commands[0].AlertID}
	closer.projection = projection
	repo.failCAS, repo.failLog, cache.failID = commands[1].AlertID, commands[2].AlertID, commands[3].AlertID
	results := closer.CloseAlerts(t.Context(), commands)
	for i, result := range results {
		var classified *CloseError
		if !errors.As(result.Err, &classified) {
			t.Fatalf("item %d: %v", i, result.Err)
		}
		want := 502
		if i == 1 {
			want = 409
		}
		if classified.Status != want {
			t.Fatalf("item %d status=%d", i, classified.Status)
		}
	}
	repo.failCAS, repo.failLog, cache.failID, projection.failID = "", "", "", ""
	results = closer.CloseAlerts(t.Context(), commands)
	for i, result := range results {
		if result.Err != nil || result.AlreadyClosed != (i != 1) || result.Alert.EndOperation.ID != commands[i].OperationID {
			t.Fatalf("retry item %d: %+v", i, result)
		}
		logs, err := repo.ListAlertLogs(t.Context(), "tenant", commands[i].AlertID, store.PageRequest{Limit: 10})
		if err != nil || len(logs.Logs) != 1 {
			t.Fatal("retry lost or duplicated stable close log", logs, err)
		}
	}
	if held.Load() != 0 || len(closer.slots) != 0 {
		t.Fatal("partial failure leaked lease or slot")
	}
}

func TestCloseBatchCancellationBusyAndLeaseFailure(t *testing.T) {
	closer, _, _, commands, held := closeBatchFixture(t, 8)
	ctx, cancel := context.WithCancel(t.Context())
	entered := make(chan struct{}, 1)
	closer.withProcessor = func(ctx context.Context, _ store.StoredAlert, _ func(*lifecycle.Processor) error) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}
	done := make(chan []lifecycle.CloseAlertOutcome, 1)
	go func() { done <- closer.CloseAlerts(ctx, commands) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("batch did not start")
	}
	cancel()
	select {
	case results := <-done:
		for _, result := range results {
			if !errors.Is(result.Err, context.Canceled) {
				t.Fatal("cancellation cause lost", result.Err)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("batch did not exit")
	}
	for range cap(closer.batchSlots) {
		closer.batchSlots <- struct{}{}
	}
	for _, result := range closer.CloseAlerts(t.Context(), commands) {
		var classified *CloseError
		if !errors.As(result.Err, &classified) || classified.Status != 429 {
			t.Fatal(result.Err)
		}
	}
	for range cap(closer.batchSlots) {
		<-closer.batchSlots
	}
	if held.Load() != 0 || len(closer.slots) != 0 {
		t.Fatal("canceled batch leaked capacity")
	}
}

func TestCloseBatchLeaseReleaseFailurePreservesFactsAndCause(t *testing.T) {
	closer, _, _, commands, held := closeBatchFixture(t, 3)
	projection := &closeBatchProjectionStub{}
	closer.projection = projection
	prepare := closer.withProcessor
	releaseErr := errors.New("lease release failed")
	closer.withProcessor = func(ctx context.Context, identity store.StoredAlert, run func(*lifecycle.Processor) error) error {
		return errors.Join(prepare(ctx, identity, run), releaseErr)
	}
	for _, result := range closer.CloseAlerts(t.Context(), commands) {
		var classified *CloseError
		if result.Alert.Status != domain.AlertStatusClosed || !errors.As(result.Err, &classified) || classified.Status != 502 || !errors.Is(result.Err, releaseErr) {
			t.Fatal("release failure misreported", result)
		}
	}
	if projection.calls != 0 || held.Load() != 0 || len(closer.slots) != 0 {
		t.Fatal("failed lease release projected or leaked capacity")
	}
}

func TestCloseBatchSourceFailureAndBusyLeaseNeverWrite(t *testing.T) {
	for _, sourceFailed := range []bool{false, true} {
		t.Run(fmt.Sprint(sourceFailed), func(t *testing.T) {
			closer, repo, _, commands, held := closeBatchFixture(t, 4)
			closer.sources.(*closeBatchSources).failed.Store(sourceFailed)
			if !sourceFailed {
				closer.withProcessor = func(context.Context, store.StoredAlert, func(*lifecycle.Processor) error) error {
					return scheduler.ErrLockBusy
				}
			}
			for _, result := range closer.CloseAlerts(t.Context(), commands) {
				var classified *CloseError
				want := 429
				if sourceFailed {
					want = 502
				}
				if !errors.As(result.Err, &classified) || classified.Status != want {
					t.Fatal(result.Err)
				}
			}
			if repo.writes.Load() != 0 || held.Load() != 0 || len(closer.slots) != 0 {
				t.Fatal("failed preparation wrote or leaked")
			}
		})
	}
}

func TestCloseSingleRequestRunsFiveBatchesConcurrently(t *testing.T) {
	closer, repo, cache, commands, held := closeBatchFixture(t, 5000)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	var calls, active, peak atomic.Int32
	repo.afterWrite = func() {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		if calls.Add(1) <= 2 {
			entered <- struct{}{}
			<-release
		}
	}
	done := make(chan []lifecycle.CloseAlertOutcome, 1)
	go func() { done <- closer.CloseAlerts(t.Context(), commands) }()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("single request did not run two batch CAS operations concurrently")
		}
	}
	if len(closer.batchSlots) != 2 {
		t.Fatal("internal batches did not occupy global close capacity")
	}
	for _, v := range closer.CloseAlerts(t.Context(), commands[:1]) {
		var err *CloseError
		if !errors.As(v.Err, &err) || err.Status != 429 {
			t.Fatal("another request exceeded global batch budget", v.Err)
		}
	}
	unblock.Do(func() { close(release) })
	select {
	case results := <-done:
		for i, v := range results {
			if v.Err != nil || v.Alert.AlertID != commands[i].AlertID || v.Alert.Status != domain.AlertStatusClosed {
				t.Fatalf("item %d: %+v", i, v)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent batch request did not complete")
	}
	if peak.Load() != 2 || repo.reads.Load() != 10 || repo.writes.Load() != 5 || repo.logs.Load() != 5 || cache.items.Load() != 5000 || held.Load() != 0 || len(closer.batchSlots) != 0 {
		t.Fatal("batch I/O or global capacity changed", peak.Load(), repo.reads.Load(), repo.writes.Load(), repo.logs.Load(), cache.items.Load(), held.Load(), len(closer.batchSlots))
	}
}

func TestCloseConcurrentBatchesCancelAndLeaveUnstartedItems(t *testing.T) {
	closer, repo, _, commands, held := closeBatchFixture(t, 6)
	closer.batchSize = 2
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{}, 2)
	closer.withProcessor = func(ctx context.Context, _ store.StoredAlert, _ func(*lifecycle.Processor) error) error {
		entered <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	done := make(chan []lifecycle.CloseAlertOutcome, 1)
	go func() { done <- closer.CloseAlerts(ctx, commands) }()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("concurrent batches not started")
		}
	}
	cancel()
	select {
	case results := <-done:
		for i, v := range results {
			if i < 4 {
				if !errors.Is(v.Err, context.Canceled) {
					t.Fatal("started item lost cancellation", i, v.Err)
				}
			} else {
				var e *CloseError
				if !errors.As(v.Err, &e) || e.Code != "not_attempted" {
					t.Fatal("unstarted item misreported", i, v.Err)
				}
			}
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled batches did not exit")
	}
	if held.Load() != 0 || len(closer.batchSlots) != 0 || repo.writes.Load() != 0 {
		t.Fatal("cancelled batches leaked resources or wrote state")
	}
}

func TestCloseConcurrentBatchesKeepIndependentFailuresAndStableRetry(t *testing.T) {
	closer, repo, cache, commands, held := closeBatchFixture(t, 6)
	closer.batchSize = 2
	repo.failCAS, repo.failLog, cache.failID = commands[0].AlertID, commands[2].AlertID, commands[4].AlertID
	results := closer.CloseAlerts(t.Context(), commands)
	for i, v := range results {
		if (v.Err != nil) != (i%2 == 0) {
			t.Fatal("one batch failure affected another member", i, v.Err)
		}
	}
	repo.failCAS, repo.failLog, cache.failID = "", "", ""
	for i, v := range closer.CloseAlerts(t.Context(), commands) {
		if v.Err != nil || v.AlreadyClosed != (i != 0) {
			t.Fatal("concurrent retry lost original close result", i, v)
		}
		logs, err := repo.ListAlertLogs(t.Context(), "tenant", commands[i].AlertID, store.PageRequest{Limit: 10})
		if err != nil || len(logs.Logs) != 1 {
			t.Fatal("retry duplicated or lost close log", i, logs, err)
		}
	}
	if held.Load() != 0 || len(closer.batchSlots) != 0 {
		t.Fatal("partial failures leaked batch resources")
	}
}
