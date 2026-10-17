// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package redisstate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRedisDependencyCandidateOrderingTenantAndSharedInstances(t *testing.T) {
	s := redisClipStore(t)
	other := &Store{client: s.client, namespace: s.namespace}
	p := dependencyFixture().Policy
	at := time.Date(2026, 10, 14, 1, 2, 3, 123456789, time.UTC)
	update := func(id string, at time.Time, revision int64) DependencyCandidateUpdate {
		return DependencyCandidateUpdate{TenantID: "tenant", AlertID: id, Revision: revision, BeginAt: at, Memberships: []DependencyMembership{{Policy: p, Matched: true}}}
	}
	if err := s.ApplyDependencyCandidateBatch(t.Context(), []DependencyCandidateUpdate{update("z", at, 1), update("a", at, 1), update("older", at.Add(-time.Nanosecond), 1)}); err != nil {
		t.Fatal(err)
	}
	if _, ready, err := other.TopDependencyCandidate(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p}); err != nil || ready {
		t.Fatal("unready candidate became usable", ready, err)
	}
	if err := s.MarkDependencyIndexReady(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p}); err != nil {
		t.Fatal(err)
	}
	first, ready, err := other.TopDependencyCandidate(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p})
	if err != nil || !ready || first.AlertID != "a" {
		t.Fatalf("wrong time tie: %+v %t %v", first, ready, err)
	}
	if err := other.ApplyDependencyCandidates(t.Context(), update("newest", at.Add(time.Nanosecond), 1)); err != nil {
		t.Fatal(err)
	}
	first, _, err = s.TopDependencyCandidate(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p})
	if err != nil || first.AlertID != "newest" {
		t.Fatal("lost nanosecond ordering", first, err)
	}
	if _, ready, err := s.TopDependencyCandidate(t.Context(), "other-tenant", DependencyIndexRef{PolicyVersion: p}); err != nil || ready {
		t.Fatal("tenant leaked", err)
	}
	version := p
	version.Version++
	if _, ready, err := s.TopDependencyCandidate(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: version}); err != nil || ready {
		t.Fatal("policy version leaked", err)
	}
	if err := s.MarkDependencyIndexReady(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: version}); err != nil {
		t.Fatal(err)
	}
	if row, ready, err := s.TopDependencyCandidate(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: version}); err != nil || !ready || row.AlertID != "" {
		t.Fatal("complete empty index unavailable", row, ready, err)
	}
}

func TestRedisDependencyCandidateRevisionProtectsLiveStateFromBackfill(t *testing.T) {
	s := redisClipStore(t)
	p := dependencyFixture().Policy
	at := time.Now().UTC()
	u := DependencyCandidateUpdate{TenantID: "tenant", AlertID: "main", Revision: 1, BeginAt: at, Memberships: []DependencyMembership{{Policy: p, Matched: true}}}
	if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDependencyIndexReady(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p}); err != nil {
		t.Fatal(err)
	}
	stale, _, err := s.TopDependencyCandidate(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p})
	if err != nil {
		t.Fatal(err)
	}
	u.Revision = 2
	if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveDependencyCandidate(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p}, stale); err != nil {
		t.Fatal(err)
	}
	current, _, err := s.TopDependencyCandidate(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p})
	if err != nil || current.Revision != 2 || current.AlertID != "main" {
		t.Fatal("old observed removal deleted newer main", current, err)
	}
	ended := u
	ended.Revision = 3
	ended.Clear = true
	ended.Memberships = nil
	if err := s.ApplyDependencyCandidates(t.Context(), ended); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if row, _, err := s.TopDependencyCandidate(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p}); err != nil || row.AlertID != "" {
		t.Fatal("late backfill resurrected terminal", row, err)
	}
	// 同 revision 的新版本回填可补齐成员，但不能丢掉另一个已求值版本。
	u.Revision = 4
	if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	p2 := p
	p2.Version++
	u.Memberships = []DependencyMembership{{Policy: p2, Matched: true}}
	if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if row, _, err := s.TopDependencyCandidate(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p}); err != nil || row.AlertID != "main" {
		t.Fatal("new version erased current version", row, err)
	}
	// 目标集合改变后，以相同 Alert revision 显式撤销当前版本资格。
	u.Memberships = []DependencyMembership{{Policy: p, Matched: false}}
	if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if row, _, err := s.TopDependencyCandidate(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p}); err != nil || row.AlertID != "" {
		t.Fatal("target change retained old eligibility", row, err)
	}
}

func TestRedisDependencyCandidateConcurrentRevisions(t *testing.T) {
	s := redisClipStore(t)
	p := dependencyFixture().Policy
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for revision := range int64(32) {
		wg.Go(func() {
			errs <- s.ApplyDependencyCandidates(t.Context(), DependencyCandidateUpdate{TenantID: "tenant", AlertID: "main", Revision: revision + 1, BeginAt: time.Unix(100, 0), Memberships: []DependencyMembership{{Policy: p, Matched: (revision+1)%2 == 0}}})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MarkDependencyIndexReady(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p}); err != nil {
		t.Fatal(err)
	}
	row, ready, err := s.TopDependencyCandidate(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p})
	if err != nil || !ready || row.AlertID != "main" || row.Revision != 32 {
		t.Fatalf("revision order lost: %+v %t %v", row, ready, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := s.TopDependencyCandidate(ctx, "tenant", DependencyIndexRef{PolicyVersion: p}); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestRedisDependencyCandidateUsesSourceReadTimeWithinAlertRevision(t *testing.T) {
	s := redisClipStore(t)
	other := &Store{client: s.client, namespace: s.namespace}
	p := dependencyFixture().Policy
	old := time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC)
	newer := old.Add(30 * time.Second)
	u := DependencyCandidateUpdate{TenantID: "tenant", AlertID: "main", Revision: 1, BeginAt: old, Memberships: []DependencyMembership{{Policy: p, Matched: true, ObservedAt: old}}}
	if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDependencyIndexReady(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p}); err != nil {
		t.Fatal(err)
	}
	// 控制面发现目标已移除，旧 Worker 的命中不能复活这个成员。
	u.Memberships[0] = DependencyMembership{Policy: p, Matched: false, ObservedAt: newer}
	if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	u.Memberships[0] = DependencyMembership{Policy: p, Matched: true, ObservedAt: old}
	if err := other.ApplyDependencyCandidates(t.Context(), u); !errors.Is(err, ErrDependencySnapshotStale) {
		t.Fatal("old source should stay pending", err)
	}
	if row, _, err := s.TopDependencyCandidate(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p}); err != nil || row.AlertID != "" {
		t.Fatal("old source snapshot resurrected member", row, err)
	}
	// 随后新快照重新纳入目标，旧 Worker 的未命中也不能删除新成员。
	u.Memberships[0] = DependencyMembership{Policy: p, Matched: true, ObservedAt: newer.Add(30 * time.Second)}
	if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	u.Memberships[0] = DependencyMembership{Policy: p, Matched: false, ObservedAt: newer}
	if err := other.ApplyDependencyCandidates(t.Context(), u); !errors.Is(err, ErrDependencySnapshotStale) {
		t.Fatal("old source should stay pending", err)
	}
	if row, _, err := s.TopDependencyCandidate(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p}); err != nil || row.AlertID != "main" {
		t.Fatal("old source snapshot deleted new member", row, err)
	}
	// CAS 领先不能让旧目标快照删除新资格；队列保留后刷新依赖再修复索引。
	u.Revision = 2
	if err := other.ApplyDependencyCandidates(t.Context(), u); !errors.Is(err, ErrDependencySnapshotStale) {
		t.Fatal("old source should stay pending", err)
	}
	if row, _, err := s.TopDependencyCandidate(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p}); err != nil || row.AlertID != "main" || row.Revision != 1 {
		t.Fatal("new Alert revision using old source removed member", row, err)
	}
}

func TestDependencyCandidateMemberPreservesTimestampAndIDOrder(t *testing.T) {
	at := time.Date(2026, 10, 14, 1, 2, 3, 123456789, time.UTC)
	if dependencyCandidateMember(at.Add(time.Nanosecond), "z") >= dependencyCandidateMember(at, "a") {
		t.Fatal("newer nanosecond sorted after old")
	}
	if dependencyCandidateMember(at, "a") >= dependencyCandidateMember(at, "z") {
		t.Fatal("equal timestamp ID order reversed")
	}
}

func TestRedisDependencyCandidateConfigNamespacesAndRetirement(t *testing.T) {
	s := redisClipStore(t)
	p := dependencyFixture().Policy
	old := DependencyIndexRef{PolicyVersion: p, ConfigDigest: "old"}
	current := DependencyIndexRef{PolicyVersion: p, ConfigDigest: "current"}
	at := time.Now().UTC()
	write := func(digest string, revision int64) {
		t.Helper()
		if err := s.ApplyDependencyCandidates(t.Context(), DependencyCandidateUpdate{TenantID: "tenant", AlertID: "main", ConfigDigest: digest, Revision: revision, BeginAt: at, Memberships: []DependencyMembership{{Policy: p, Matched: true, ObservedAt: at}}}); err != nil {
			t.Fatal(err)
		}
	}
	write("current", 2)
	if err := s.MarkDependencyIndexReady(t.Context(), "tenant", current); err != nil {
		t.Fatal(err)
	}
	if _, ready, err := s.TopDependencyCandidate(t.Context(), "tenant", old); err != nil || ready {
		t.Fatal("new config leaked Ready", ready, err)
	}
	// 旧 Worker 的更高 Alert revision 只能维护其配置 namespace，不能清掉当前配置成员。
	write("old", 3)
	if row, ready, err := s.TopDependencyCandidate(t.Context(), "tenant", current); err != nil || !ready || row.AlertID != "main" || row.Revision != 2 {
		t.Fatal("old config erased current config", row, ready, err)
	}
	if err := s.MarkDependencyIndexReady(t.Context(), "tenant", old); err != nil {
		t.Fatal(err)
	}
	if err := s.RetireDependencyNamespace(t.Context(), "tenant", "old"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDependencyIndexReady(t.Context(), "tenant", old); err != nil {
		t.Fatal(err)
	}
	if _, ready, err := s.TopDependencyCandidate(t.Context(), "tenant", old); err != nil || ready {
		t.Fatal("late old backfill reopened retired namespace", ready, err)
	}
	// 回退同一 Digest 的控制面先完整回填，最后激活；回填期间保留 pending。
	write("old", 4)
	if _, ready, err := s.TopDependencyCandidate(t.Context(), "tenant", old); err != nil || ready {
		t.Fatal("rollback became usable before activate", ready, err)
	}
	if err := s.ActivateDependencyNamespace(t.Context(), "tenant", "old"); err != nil {
		t.Fatal(err)
	}
	if row, ready, err := s.TopDependencyCandidate(t.Context(), "tenant", old); err != nil || !ready || row.Revision != 4 {
		t.Fatal("rollback remained retired", row, ready, err)
	}
	if err := s.ApplyDependencyCandidates(t.Context(), DependencyCandidateUpdate{TenantID: "tenant", AlertID: "main", Revision: 5, Clear: true}); err != nil {
		t.Fatal(err)
	}
	write("old", 4)
	write("current", 4)
	for _, ref := range []DependencyIndexRef{old, current} {
		if row, _, err := s.TopDependencyCandidate(t.Context(), "tenant", ref); err != nil || row.AlertID != "" {
			t.Fatal("terminal did not clear all namespaces", ref, row, err)
		}
	}
}

func TestRedisDependencySourceFloorSurvivesLocalFalseAndNewRevision(t *testing.T) {
	s := redisClipStore(t)
	p := dependencyFixture().Policy
	p2 := p
	p2.Version++
	ref := DependencyIndexRef{PolicyVersion: p}
	at := time.Now().UTC()
	u := DependencyCandidateUpdate{TenantID: "tenant", AlertID: "main", Revision: 1, BeginAt: at, Memberships: []DependencyMembership{{Policy: p, Matched: true, ObservedAt: at}}}
	if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDependencyIndexReady(t.Context(), "tenant", ref); err != nil {
		t.Fatal(err)
	}
	u.Revision = 2
	u.Memberships = []DependencyMembership{{Policy: p, Matched: false}}
	if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	// 同 revision 外部命中不能推翻纯字段已确定 false。
	u.Memberships = []DependencyMembership{{Policy: p, Matched: true, ObservedAt: at.Add(time.Second)}}
	if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if row, _, err := s.TopDependencyCandidate(t.Context(), "tenant", ref); err != nil || row.AlertID != "" {
		t.Fatal("remote result overrode local false", row, err)
	}
	// 更高 revision 的旧源不能借纯字段 false 丢掉的成员清理时间 floor。
	u.Revision = 3
	u.Memberships = []DependencyMembership{{Policy: p, Matched: true, ObservedAt: at.Add(-time.Second)}, {Policy: p2, Matched: true, ObservedAt: at}}
	if err := s.ApplyDependencyCandidates(t.Context(), u); !errors.Is(err, ErrDependencySnapshotStale) {
		t.Fatal("stale higher revision did not stay pending", err)
	}
	if err := s.MarkDependencyIndexReady(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p2}); err != nil {
		t.Fatal(err)
	}
	if row, _, err := s.TopDependencyCandidate(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p2}); err != nil || row.AlertID != "" {
		t.Fatal("partial update happened before source preflight", row, err)
	}
	u.Memberships[0].ObservedAt = at.Add(time.Second)
	if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if row, _, err := s.TopDependencyCandidate(t.Context(), "tenant", ref); err != nil || row.AlertID != "main" || row.Revision != 3 {
		t.Fatal("fresh retry did not repair saved revision", row, err)
	}
}

func TestRedisDependencySourceFloorHistoryStaysOutsideCurrentMemberScan(t *testing.T) {
	s := redisClipStore(t)
	p := dependencyFixture().Policy
	at := time.Now().UTC()
	u := DependencyCandidateUpdate{TenantID: "tenant", AlertID: "main", Revision: 1, BeginAt: at, Memberships: []DependencyMembership{{Policy: p, Matched: true, ObservedAt: at}}}
	if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	owner := s.dependencyCandidateOwner("tenant", "main", "")
	historical := map[string]any{}
	for i := range 2048 {
		historical["old-policy-"+fmt.Sprint(i)] = at.Format("20060102150405.000000000")
	}
	if err := s.client.HSet(t.Context(), owner+":times", historical).Err(); err != nil {
		t.Fatal(err)
	}
	u.Revision = 2
	if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	fields, err := s.client.HGetAll(t.Context(), owner).Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 {
		t.Fatal("current owner contains source history scanned by revision updates", len(fields))
	}
	count, err := s.client.HLen(t.Context(), owner+":times").Result()
	if err != nil || count != 2049 {
		t.Fatal("source floors lost across revision", count, err)
	}
	if strings.Contains(applyDependencyCandidateScript, "HGETALL',KEYS[3]") || strings.Contains(applyDependencyCandidateScript, "HKEYS',KEYS[3]") {
		t.Fatal("normal updates scan source history")
	}
	if err := s.MarkDependencyIndexReady(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p}); err != nil {
		t.Fatal(err)
	}
	if row, _, err := s.TopDependencyCandidate(t.Context(), "tenant", DependencyIndexRef{PolicyVersion: p}); err != nil || row.Revision != 2 || !row.ObservedAt.Equal(at) {
		t.Fatal("Top did not read independent source timestamp", row, err)
	}
	u.Clear = true
	u.Revision = 3
	if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if count, err := s.client.Exists(t.Context(), owner, owner+":times").Result(); err != nil || count != 0 {
		t.Fatal("terminal retained member or source history", count, err)
	}
}
