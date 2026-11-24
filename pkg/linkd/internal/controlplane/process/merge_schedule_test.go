// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License").

package controlplaneprocess

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"testing/synctest"
)

type scheduledMergeDue struct {
	scores   map[string]float64
	calls    int
	fail     bool
	failCall int
}

func (d *scheduledMergeDue) MergeDueScores(_ context.Context, tenant string, ids []string) ([]float64, error) {
	d.calls++
	if len(ids) > 16 {
		return nil, errors.New("due query exceeds Redis budget")
	}
	if d.fail || d.calls == d.failCall {
		return nil, errors.New("redis unavailable")
	}
	result := make([]float64, len(ids))
	for i, id := range ids {
		result[i] = d.scores[tenant+":"+id]
	}
	return result, nil
}

func TestMergeWaitingScanRetriesFailedSecondChunk(t *testing.T) {
	due := &scheduledMergeDue{failCall: 2}
	scan := &mergeWaitingScan{due: due}
	page := store.MergeWorkPage{}
	for i := range 17 {
		page.Items = append(page.Items, store.MergeWorkItem{Alert: store.StoredAlert{Alert: domain.Alert{BKTenantID: "tenant"}}, WindowID: fmt.Sprintf("w-%d", i)})
	}
	if _, err := scan.selectWork(t.Context(), page, time.Now()); err == nil || len(scan.seen) != 0 || len(scan.checked) != 0 || scan.round != 0 {
		t.Fatal("failed chunk consumed page")
	}
	got, err := scan.selectWork(t.Context(), page, time.Now())
	if err != nil || len(got) != 17 || scan.round != 1 {
		t.Fatal("failed page did not recover", err)
	}
}

func TestMergeWaitingExpiredMembersShareWindowJob(t *testing.T) {
	now := time.Now()
	scan := &mergeWaitingScan{due: &scheduledMergeDue{scores: map[string]float64{"tenant:window": float64(now.UnixMilli())}}}
	for pageNumber := range 2 {
		page := store.MergeWorkPage{Next: store.MergeWorkCursor{TenantID: "tenant"}}
		for i := range 3 {
			page.Items = append(page.Items, store.MergeWorkItem{WindowID: "window", Alert: store.StoredAlert{Alert: domain.Alert{BKTenantID: "tenant", AlertID: fmt.Sprint(i), Merge: &domain.AlertMerge{Pending: []domain.MergeWait{{WindowID: "window", Deadline: now.Add(-time.Second)}}}}}})
		}
		items, err := scan.selectWork(t.Context(), page, now)
		if err != nil || len(items) != 3 {
			t.Fatalf("expired members omitted: page=%d count=%d error=%v", pageNumber, len(items), err)
		}
		members := 0
		jobs, err := mergeWaitingJobs(items, 0, func(_ context.Context, batch []store.MergeWorkItem, _ time.Time) error {
			members += len(batch)
			return nil
		})
		if err != nil || len(jobs) != 1 {
			t.Fatal("members did not share one window lease", err)
		}
		if err := jobs[0].run(t.Context()); err != nil || members != 3 {
			t.Fatalf("group lost members: members=%d error=%v", members, err)
		}
	}
}

func TestMergeWaitingJobsRotateAfterWholeJobTimeout(t *testing.T) {
	for _, test := range []struct {
		initial int
		order   string
	}{
		{initial: 0, order: "a,b,healthy"},
		{initial: store.MergeWorkBatchSize - 1, order: "b,a,b,healthy"},
	} {
		t.Run(fmt.Sprint(test.initial), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				scan := &mergeWaitingScan{due: &scheduledMergeDue{}, round: test.initial}
				page := store.MergeWorkPage{}
				for _, tenant := range []string{"one", "two"} {
					for _, id := range []string{"a", "b", "healthy"} {
						page.Items = append(page.Items, store.MergeWorkItem{Alert: store.StoredAlert{Alert: domain.Alert{BKTenantID: tenant, AlertID: id}}, WindowID: strings.Repeat("a", 64)})
					}
				}
				first := map[string][]string{}
				healthy := map[string]bool{}
				runner := &mergeJobRunner{slots: make(chan struct{}, 4), locker: &mergeTestLocker{}}
				for range strings.Split(test.order, ",") {
					round := scan.round
					items, err := scan.selectWork(t.Context(), page, time.Now())
					if err != nil {
						t.Fatal(err)
					}
					jobs, err := mergeWaitingJobs(items, round, func(ctx context.Context, batch []store.MergeWorkItem, _ time.Time) error {
						tenant := batch[0].Alert.Alert.BKTenantID
						first[tenant] = append(first[tenant], batch[0].Alert.Alert.AlertID)
						for _, item := range batch {
							if item.Alert.Alert.AlertID == "healthy" {
								healthy[tenant] = true
								continue
							}
							<-ctx.Done()
							return ctx.Err()
						}
						return nil
					})
					if err != nil {
						t.Fatal(err)
					}
					for _, job := range jobs {
						if err := runner.run(t.Context(), job); !errors.Is(err, context.DeadlineExceeded) {
							t.Fatal("job timeout lost", err)
						}
					}
				}
				for _, tenant := range []string{"one", "two"} {
					if !healthy[tenant] || strings.Join(first[tenant], ",") != test.order {
						t.Fatal("later member starved across rounds", tenant, first[tenant])
					}
				}
				if page.Items[0].Alert.Alert.AlertID != "a" {
					t.Fatal("rotation modified scan page")
				}
			})
		})
	}
}

func TestMergeWaitingScanChunksFullPage(t *testing.T) {
	for _, count := range []int{16, 17, 128} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			due := &scheduledMergeDue{}
			scan := &mergeWaitingScan{due: due}
			page := store.MergeWorkPage{}
			for i := range count {
				page.Items = append(page.Items, store.MergeWorkItem{Alert: store.StoredAlert{Alert: domain.Alert{BKTenantID: "tenant"}}, WindowID: fmt.Sprintf("window-%d", i)})
			}
			got, err := scan.selectWork(t.Context(), page, time.Now())
			if err != nil || len(got) != count {
				t.Fatalf("selected=%d/%d error=%v", len(got), count, err)
			}
		})
	}
}

func TestMergeWaitingScanKeepsLostWindowMembersAcrossPages(t *testing.T) {
	scan := &mergeWaitingScan{due: &scheduledMergeDue{}}
	for pageNumber := range 2 {
		page := store.MergeWorkPage{}
		for i := range 3 {
			page.Items = append(page.Items, store.MergeWorkItem{Alert: store.StoredAlert{Alert: domain.Alert{BKTenantID: "tenant", AlertID: fmt.Sprintf("member-%d-%d", pageNumber, i)}}, WindowID: "lost-window"})
		}
		if pageNumber == 0 {
			page.Next = store.MergeWorkCursor{TenantID: "tenant"}
		}
		got, err := scan.selectWork(t.Context(), page, time.Now())
		if err != nil || len(got) != len(page.Items) {
			t.Fatalf("member compensation deduplicated: page=%d selected=%d error=%v", pageNumber, len(got), err)
		}
	}
}

func TestMergeWaitingScanDeduplicatesPagesAndUsesDueHints(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	due := &scheduledMergeDue{scores: map[string]float64{"tenant:window": float64(now.Add(time.Hour).UnixMilli())}}
	scan := &mergeWaitingScan{due: due}
	item := store.MergeWorkItem{Alert: store.StoredAlert{Alert: domain.Alert{BKTenantID: "tenant"}}, WindowID: "window"}
	first := store.MergeWorkPage{Items: []store.MergeWorkItem{item, item}, Next: store.MergeWorkCursor{TenantID: "tenant"}}
	got, err := scan.selectWork(t.Context(), first, now)
	if err != nil || len(got) != 1 || due.calls != 1 {
		t.Fatal("first compensation did not select one window", err)
	}
	last := store.MergeWorkPage{Items: []store.MergeWorkItem{item}}
	got, err = scan.selectWork(t.Context(), last, now)
	if err != nil || len(got) != 0 || due.calls != 1 {
		t.Fatal("same window repeated across pages", err)
	}
	got, err = scan.selectWork(t.Context(), last, now.Add(time.Second))
	if err != nil || len(got) != 0 {
		t.Fatal("not-ready window reread each sweep", err)
	}
	due.scores["tenant:window"] = float64(now.UnixMilli())
	got, err = scan.selectWork(t.Context(), last, now.Add(2*time.Second))
	if err != nil || len(got) != 1 {
		t.Fatal("new ready hint ignored", err)
	}
	due.scores["tenant:window"] = float64(now.Add(time.Hour).UnixMilli())
	got, err = scan.selectWork(t.Context(), last, now.Add(63*time.Second))
	if err != nil || len(got) != 1 {
		t.Fatal("dependency/recovery compensation starved", err)
	}
	if _, err := scan.selectWork(t.Context(), store.MergeWorkPage{}, now.Add(time.Minute)); err != nil || len(scan.checked) != 0 {
		t.Fatal("ended window retained", err)
	}
}

func TestMergeWaitingScanKeepsAllIntentsAndRetriesFailedPage(t *testing.T) {
	due := &scheduledMergeDue{fail: true}
	scan := &mergeWaitingScan{due: due}
	window := store.MergeWorkItem{Alert: store.StoredAlert{Alert: domain.Alert{BKTenantID: "tenant"}}, WindowID: "window"}
	intent := store.MergeWorkItem{Alert: window.Alert}
	page := store.MergeWorkPage{Items: []store.MergeWorkItem{intent, intent, window}}
	if _, err := scan.selectWork(t.Context(), page, time.Now()); err == nil || len(scan.seen) != 0 {
		t.Fatal("failed page consumed cursor")
	}
	due.fail = false
	got, err := scan.selectWork(t.Context(), page, time.Now())
	if err != nil || len(got) != 3 {
		t.Fatal("member intents deduplicated as window", err)
	}
}
