// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycleprocess

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/store"
	storeassembly "linkd/internal/store/assembly"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type explicitMergeRepository struct {
	*memory.Repository
	reads, writes, logs, confirms atomic.Int32
	failCAS, failLog, failConfirm string
	lostResponse                  bool
	afterWrite                    func()
	afterConfirm                  func()
}

func (r *explicitMergeRepository) GetAlertsCurrent(ctx context.Context, tenant string, ids []string) (store.AlertBatch, error) {
	r.reads.Add(1)
	return r.GetAlerts(ctx, tenant, ids)
}

func (r *explicitMergeRepository) CompareAndSetAlertsFromSnapshots(ctx context.Context, updates []store.AlertSnapshotUpdate) ([]store.AlertUpdateResult, error) {
	r.writes.Add(1)
	results := make([]store.AlertUpdateResult, len(updates))
	for i, u := range updates {
		if u.Current.Alert.AlertID == r.failCAS {
			results[i].Err = store.ErrVersionConflict
			continue
		}
		results[i].StoredAlert, results[i].Err = r.CompareAndSetAlert(ctx, u.Current.Alert.BKTenantID, u.Current.Alert.AlertID, u.Current.Version, u.Replacement)
	}
	if r.afterWrite != nil {
		r.afterWrite()
	}
	return results, nil
}

func (r *explicitMergeRepository) CompleteAlertChanges(ctx context.Context, current []store.StoredAlert, field store.AlertChangeField) ([]store.AlertUpdateResult, error) {
	r.confirms.Add(1)
	results := make([]store.AlertUpdateResult, len(current))
	for i, c := range current {
		if c.Alert.AlertID == r.failConfirm && !r.lostResponse {
			results[i].Err = errors.New("confirmation failed")
			continue
		}
		next, _ := store.CompletedAlertChange(c, field)
		results[i].StoredAlert, results[i].Err = r.CompareAndSetAlert(ctx, c.Alert.BKTenantID, c.Alert.AlertID, c.Version, next)
		if c.Alert.AlertID == r.failConfirm && r.lostResponse {
			results[i].Err = errors.New("confirmation response lost")
		}
	}
	if r.afterConfirm != nil {
		r.afterConfirm()
	}
	return results, nil
}

func (r *explicitMergeRepository) AppendAlertLogs(ctx context.Context, logs []domain.AlertLog) ([]store.AppendAlertLogItemResult, error) {
	r.logs.Add(1)
	results := make([]store.AppendAlertLogItemResult, len(logs))
	for i, log := range logs {
		if log.AlertID == r.failLog {
			results[i].Err = errors.New("log unavailable")
			continue
		}
		results[i].Result, results[i].Err = r.AppendAlertLog(ctx, log)
	}
	return results, nil
}

func explicitMergeFixture(t *testing.T) (*MergeOperator, *explicitMergeRepository, domain.MergeRelation, []string, *atomic.Int32) {
	t.Helper()
	repo := &explicitMergeRepository{Repository: memory.New()}
	parent := storetest.Alert("tenant", "parent", "parent-event", "parent-fp", "warning")
	r := domain.MergeRelation{ID: strings.Repeat("a", 64), TenantID: "tenant", WindowID: strings.Repeat("b", 64), GroupKey: strings.Repeat("c", 64), Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: strings.Repeat("d", 64)}, ParentAlertID: parent.AlertID, ParentFingerprint: parent.Fingerprint, State: "preparing", CreatedAt: parent.CreateAt, UpdatedAt: parent.UpdateAt, IndexOffset: 5}
	parent.EventSourceID = domain.BuiltinMergeEventSourceID
	parent.Merge = &domain.AlertMerge{Role: "aggregate", State: "merged", OperationID: r.ID, RelationIDs: []string{r.ID}}
	if _, err := repo.CreateAlert(t.Context(), parent); err != nil {
		t.Fatal(err)
	}
	ids := []string{"a", "b", "c", "d"}
	for _, id := range ids {
		a := storetest.Alert("tenant", id, "opening", id, "warning")
		a.Merge = &domain.AlertMerge{Role: "original", State: "pending", Pending: []domain.MergeWait{{WindowID: r.WindowID, Policy: r.Policy, GroupKey: r.GroupKey, MemberEventID: a.TriggerEventID, Severity: a.Severity, Groups: []int{0}, StartedAt: a.UpdateAt, Deadline: a.UpdateAt.Add(time.Hour)}}}
		if _, err := repo.CreateAlert(t.Context(), a); err != nil {
			t.Fatal(err)
		}
		r.Members = append(r.Members, domain.MergeRelationMember{AlertID: id, State: "pending"})
		r.WaitMemberIDs = append(r.WaitMemberIDs, id)
	}
	active := &atomic.Int32{}
	var held sync.Map
	op := &MergeOperator{memberBatchSize: 4, resources: &AlertProcessorResources{repository: &storeassembly.Runtime{Repository: repo, Backend: config.RepositoryTypeElasticsearch}}, slots: make(chan struct{}, 4)}
	op.withProcessor = func(ctx context.Context, identity store.StoredAlert, run func(*lifecycle.Processor) error) error {
		if _, loaded := held.LoadOrStore(identity.Alert.Fingerprint, true); loaded {
			return errors.New("lease busy")
		}
		defer held.Delete(identity.Alert.Fingerprint)
		active.Add(1)
		defer active.Add(-1)
		p, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, nil, config.DefaultSeverityConfig(), lifecycle.SystemClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			return err
		}
		return run(p)
	}
	return op, repo, r, ids, active
}

func TestExplicitMergeBatchStagesAndPartialRetry(t *testing.T) {
	for _, fault := range []string{"none", "cas", "log", "confirm", "lost response"} {
		t.Run(fault, func(t *testing.T) {
			op, repo, r, ids, held := explicitMergeFixture(t)
			switch fault {
			case "cas":
				repo.failCAS = "b"
			case "log":
				repo.failLog = "b"
			case "confirm":
				repo.failConfirm = "b"
			case "lost response":
				repo.failConfirm = "b"
				repo.lostResponse = true
			}
			items := op.LinkMergeMembersPrepared(t.Context(), "tenant", ids, r)
			for i, item := range items {
				if ids[i] == "b" && fault != "none" {
					if item.Err == nil {
						t.Fatal("item failure hidden")
					}
				} else if item.Err != nil {
					t.Fatal(item.Err)
				}
			}
			if held.Load() != 0 || len(op.slots) != 0 || repo.reads.Load() != 2 || repo.writes.Load() != 1 || repo.logs.Load() != 1 || repo.confirms.Load() != 1 {
				t.Fatalf("leases or explicit batch boundaries: reads=%d writes=%d logs=%d confirms=%d held=%d", repo.reads.Load(), repo.writes.Load(), repo.logs.Load(), repo.confirms.Load(), held.Load())
			}
			failed, err := repo.GetAlert(t.Context(), "tenant", "b")
			if err != nil {
				t.Fatal(err)
			}
			pending := fault == "log" || fault == "confirm"
			if (failed.Alert.MergeChange != nil) != pending {
				t.Fatalf("pending intent after %s", fault)
			}
			repo.failCAS, repo.failLog, repo.failConfirm = "", "", ""
			items = op.LinkMergeMembersPrepared(t.Context(), "tenant", ids, r)
			for _, item := range items {
				if item.Err != nil || item.Alert.MergeChange != nil || len(item.Alert.Merge.RelationIDs) != 1 {
					t.Fatalf("retry: %+v", item)
				}
			}
		})
	}
}

func TestExplicitMergeConcurrentWindowsAndCancellationReleaseSlots(t *testing.T) {
	op, _, r, ids, held := explicitMergeFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _ = op.LinkMergeMembersPrepared(ctx, "tenant", ids, r) })
	}
	wg.Wait()
	if ctx.Err() != nil || held.Load() != 0 || len(op.slots) != 0 {
		t.Fatal("windows deadlocked or leaked slots", ctx.Err())
	}
	canceled, stop := context.WithCancel(t.Context())
	stop()
	for _, item := range op.LinkMergeMembersPrepared(canceled, "tenant", ids, r) {
		if !errors.Is(item.Err, context.Canceled) {
			t.Fatalf("canceled result: %v", item.Err)
		}
	}
}

func TestExplicitMergeUnlinkUsesTerminalParentAndRetriesIndependently(t *testing.T) {
	op, repo, r, ids, held := explicitMergeFixture(t)
	for _, item := range op.LinkMergeMembersPrepared(t.Context(), "tenant", ids, r) {
		if item.Err != nil {
			t.Fatal(item.Err)
		}
	}
	parent, err := repo.GetAlert(t.Context(), "tenant", r.ParentAlertID)
	if err != nil {
		t.Fatal(err)
	}
	next := parent.Alert.Clone()
	next.UpdateAt = next.UpdateAt.Add(time.Second)
	next.Status = domain.AlertStatusClosed
	next.EndAt = &next.UpdateAt
	next.EndType = domain.AlertEndTypeSystem
	next.EndReason = "manual"
	parent, err = repo.CompareAndSetAlert(t.Context(), "tenant", r.ParentAlertID, parent.Version, next)
	if err != nil {
		t.Fatal(err)
	}
	r.State = "ending"
	r.EndReason = "parent_ended"
	r.EndStartedAt = &next.UpdateAt
	r.UpdatedAt = next.UpdateAt
	repo.failLog = "b"
	first := op.UnlinkMergeMembersPrepared(t.Context(), "tenant", ids, r, parent)
	for i, item := range first {
		if ids[i] == "b" {
			if item.Err == nil {
				t.Fatal("missing partial failure")
			}
		} else if item.Err != nil || item.Alert.Status != domain.AlertStatusActive || item.Alert.MergeChange != nil {
			t.Fatal("healthy unlink", item.Err)
		}
	}
	repo.failLog = ""
	for _, item := range op.UnlinkMergeMembersPrepared(t.Context(), "tenant", ids, r, parent) {
		if item.Err != nil || item.Alert.MergeChange != nil || len(item.Alert.Merge.RelationIDs) != 0 || item.Alert.Admission.AdmittedAt != nil {
			t.Fatal("unlink retry changed lifecycle/admission", item.Err)
		}
	}
	if held.Load() != 0 || len(op.slots) != 0 {
		t.Fatal("unlink leaked leases")
	}
}

func TestExplicitMergeReloadsParentBetweenSubmissionBlocks(t *testing.T) {
	op, repo, r, ids, held := explicitMergeFixture(t)
	op.slots = make(chan struct{}, 2)
	op.memberBatchSize = 2
	var once sync.Once
	repo.afterWrite = func() {
		once.Do(func() {
			parent, err := repo.GetAlert(t.Context(), "tenant", r.ParentAlertID)
			if err != nil {
				t.Error(err)
				return
			}
			next := parent.Alert.Clone()
			next.UpdateAt = next.UpdateAt.Add(time.Second)
			next.Status = domain.AlertStatusClosed
			next.EndType = domain.AlertEndTypeSystem
			next.EndReason = "manual"
			next.EndAt = &next.UpdateAt
			if _, err := repo.CompareAndSetAlert(t.Context(), "tenant", r.ParentAlertID, parent.Version, next); err != nil {
				t.Error(err)
			}
		})
	}
	items := op.LinkMergeMembersPrepared(t.Context(), "tenant", ids, r)
	for i, item := range items {
		if i < 2 {
			if item.Err != nil {
				t.Fatal(item.Err)
			}
		} else if !errors.Is(item.Err, store.ErrInvalidTransition) {
			t.Fatal("later block reused stale active parent", item.Err)
		}
	}
	if held.Load() != 0 || len(op.slots) != 0 {
		t.Fatal("parent close leaked member leases")
	}
}

func TestExplicitMergeBatchSizeIsIndependentOfWorkerConcurrency(t *testing.T) {
	op, repo, r, ids, held := explicitMergeFixture(t)
	op.slots = make(chan struct{}, 1)
	for _, item := range op.LinkMergeMembersPrepared(t.Context(), "tenant", ids, r) {
		if item.Err != nil {
			t.Fatal(item.Err)
		}
	}
	if repo.reads.Load() != 2 || repo.writes.Load() != 1 || repo.confirms.Load() != 1 || held.Load() != 0 || len(op.slots) != 0 {
		t.Fatal("one worker shrank four-member physical batch or leaked leases")
	}
}

func TestExplicitMergeCancelWhilePreparingLeaseReleasesEarlierMembers(t *testing.T) {
	op, repo, relation, ids, held := explicitMergeFixture(t)
	op.slots = make(chan struct{}, 1)
	original := op.withProcessor
	second := make(chan struct{})
	var calls atomic.Int32
	op.withProcessor = func(ctx context.Context, identity store.StoredAlert, run func(*lifecycle.Processor) error) error {
		if calls.Add(1) == 2 {
			close(second)
			<-ctx.Done()
			return ctx.Err()
		}
		return original(ctx, identity, run)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan []store.AlertUpdateResult, 1)
	go func() { done <- op.LinkMergeMembersPrepared(ctx, "tenant", ids, relation) }()
	select {
	case <-second:
	case <-time.After(3 * time.Second):
		t.Fatal("second lease not reached")
	}
	cancel()
	select {
	case results := <-done:
		for _, result := range results {
			if !errors.Is(result.Err, context.Canceled) {
				t.Fatal("cancellation lost", result.Err)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("lease preparation cancellation deadlocked")
	}
	if held.Load() != 0 || len(op.slots) != 0 || repo.writes.Load() != 0 {
		t.Fatal("cancellation leaked lease/slot or submitted batch")
	}
}

func TestMySQLMergeCompletionDoesNotQueueDisabledCache(t *testing.T) {
	op, repo, relation, ids, held := explicitMergeFixture(t)
	op.resources.repository.Backend = config.RepositoryTypeMySQL
	repo.afterConfirm = func() {
		for range cap(op.slots) {
			op.slots <- struct{}{}
		}
	}
	defer func() {
		for len(op.slots) > 0 {
			<-op.slots
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	results := op.LinkMergeMembersPrepared(ctx, "tenant", ids, relation)
	for _, item := range results {
		if item.Err != nil {
			t.Fatal("disabled cache waited for I/O permit", item.Err)
		}
	}
	if held.Load() != 0 {
		t.Fatal("member leases leaked")
	}
}

func TestMergeCacheBatchDoesNotQueueBehindMemberSlots(t *testing.T) {
	op, repo, relation, ids, held := explicitMergeFixture(t)
	op.resources.repository.Backend = config.RepositoryTypeElasticsearch
	repo.afterConfirm = func() {
		for range cap(op.slots) {
			op.slots <- struct{}{}
		}
	}
	defer func() {
		for len(op.slots) > 0 {
			<-op.slots
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	results := op.LinkMergeMembersPrepared(ctx, "tenant", ids, relation)
	for _, item := range results {
		if item.Err != nil {
			t.Fatal("explicit cache batch waited for member permit", item.Err)
		}
	}
	if held.Load() != 0 {
		t.Fatal("member leases leaked")
	}
}

type mergeCacheProbe struct {
	lifecycle.NoopRecentAlertCache
	batches     atomic.Int32
	fail        string
	source      string
	wrongSource atomic.Bool
}

func (c *mergeCacheProbe) PutCurrentSnapshots(_ context.Context, items []store.StoredAlert) []error {
	c.batches.Add(1)
	result := make([]error, len(items))
	for i, item := range items {
		if item.Alert.EventSourceID != c.source {
			c.wrongSource.Store(true)
		}
		if item.Alert.AlertID == c.fail {
			result[i] = errors.New("cache unavailable")
		}
	}
	return result
}

func TestMergeCompletionCacheGroupsSourcesAndRetriesCompletedMember(t *testing.T) {
	op, repo, relation, ids, held := explicitMergeFixture(t)
	original, err := repo.GetAlert(t.Context(), "tenant", ids[0])
	if err != nil {
		t.Fatal(err)
	}
	extra := original.Alert.Clone()
	extra.AlertID = "extra"
	extra.Fingerprint = "extra"
	extra.EventSourceID = "other-source"
	if _, err := repo.CreateAlert(t.Context(), extra); err != nil {
		t.Fatal(err)
	}
	ids = append(ids, extra.AlertID)
	relation.Members = append(relation.Members, domain.MergeRelationMember{AlertID: extra.AlertID, State: "pending"})
	relation.WaitMemberIDs = append(relation.WaitMemberIDs, extra.AlertID)
	op.memberBatchSize = 8
	probes := map[string]*mergeCacheProbe{original.Alert.EventSourceID: {source: original.Alert.EventSourceID, fail: "b"}, extra.EventSourceID: {source: extra.EventSourceID}}
	op.withProcessor = func(ctx context.Context, identity store.StoredAlert, run func(*lifecycle.Processor) error) error {
		held.Add(1)
		defer held.Add(-1)
		p, err := lifecycle.NewProcessor(repo, probes[identity.Alert.EventSourceID], lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, nil, config.DefaultSeverityConfig(), lifecycle.SystemClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			return err
		}
		return run(p)
	}
	first := op.LinkMergeMembersPrepared(t.Context(), "tenant", ids, relation)
	for i, item := range first {
		if (item.Err != nil) != (ids[i] == "b") {
			t.Fatal("cache partial failure", ids[i], item.Err)
		}
	}
	for _, probe := range probes {
		if probe.batches.Load() != 1 || probe.wrongSource.Load() {
			t.Fatal("cache sources mixed or fragmented")
		}
		probe.fail = ""
	}
	writes, logs := repo.writes.Load(), repo.logs.Load()
	for _, item := range op.LinkMergeMembersPrepared(t.Context(), "tenant", []string{"b"}, relation) {
		if item.Err != nil {
			t.Fatal("completed cache retry", item.Err)
		}
	}
	if probes[original.Alert.EventSourceID].batches.Load() != 2 || repo.writes.Load() != writes || repo.logs.Load() != logs || held.Load() != 0 {
		t.Fatal("cache retry repeated business writes or did not repair cache")
	}
}
