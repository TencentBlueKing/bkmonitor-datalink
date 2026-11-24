// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package activeindex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/store/storetest"
)

type rowsReader struct {
	rows  []Row
	after func()
	err   error
	calls *int
}

func (r rowsReader) ScanActiveIndex(ctx context.Context, q Query, consume func([]Row) error) error {
	if r.calls != nil {
		*r.calls++
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	page := []Row{}
	for _, row := range r.rows {
		if !slices.Contains(q.Sources, row.EventSourceID) || (q.BKTenantID != "" && q.BKTenantID != row.BKTenantID) || (len(q.Fingerprints) > 0 && !slices.Contains(q.Fingerprints, row.Fingerprint)) {
			continue
		}
		page = append(page, row)
		if len(page) >= q.PageSize {
			if err := consume(page); err != nil {
				return err
			}
			page = nil
		}
	}
	if len(page) > 0 {
		if err := consume(page); err != nil {
			return err
		}
	}
	if len(q.Fingerprints) == 0 && r.after != nil {
		r.after()
	}
	return r.err
}

func integrationStore(tb testing.TB) (*Store, *redis.Client) {
	tb.Helper()
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		tb.Skip("LINKD_TEST_REDIS_ADDRESS is not set")
	}
	client := redis.NewClient(&redis.Options{Addr: address, ContextTimeoutEnabled: true, MaxRetries: -1})
	if err := client.Ping(tb.Context()).Err(); err != nil {
		tb.Fatal(err)
	}
	id := uuid.NewString()
	source := checkSource()
	source.ActiveAlertLimits.Rules[0].KeyPrefix = "linkd-test:" + id
	source.ActiveAlertLimits.Rules[0].Notify = &config.ActiveAlertLimitNotify{Channel: "linkd-test:" + id + ":changes", Fields: map[string]string{"bk_tenant_id": "bk_tenant_id", "strategy_id": "labels.strategy_id"}}
	s := New(client, id, source, nil)
	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		for _, pattern := range []string{s.namespace + "*", "linkd-test:" + id + "*"} {
			var cursor uint64
			for {
				keys, next, err := client.Scan(ctx, cursor, pattern, 100).Result()
				if err != nil {
					break
				}
				if len(keys) > 0 {
					_ = client.Unlink(ctx, keys...).Err()
				}
				cursor = next
				if cursor == 0 {
					break
				}
			}
		}
		_ = client.Close()
	})
	return s, client
}

func integrationAlert(id string) domain.Alert {
	a := storetest.Alert("tenant", id, "event", id, "warning")
	a.EventSourceID = "source"
	a.Labels = domain.DimensionMap{"strategy_id": domain.NewStringScalar("123")}
	return a
}

func assertOnlySets(t *testing.T, s *Store, expected ...string) {
	t.Helper()
	keys := []string{}
	for _, pattern := range []string{s.namespace + "*", s.settings.Rules[0].KeyPrefix + "*"} {
		var cursor uint64
		for {
			page, next, err := s.client.Scan(t.Context(), cursor, pattern, 100).Result()
			if err != nil {
				t.Fatal(err)
			}
			keys = append(keys, page...)
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	slices.Sort(expected)
	if !slices.Equal(keys, expected) {
		t.Fatal("unexpected Redis keys", keys, expected)
	}
	for _, key := range keys {
		if kind := s.client.Type(t.Context(), key).Val(); kind != "set" {
			t.Fatal(key, kind)
		}
	}
}

func TestActiveCacheRedisMembershipNotifications(t *testing.T) {
	s, client := integrationStore(t)
	ctx := t.Context()
	a := integrationAlert("first")
	g, _ := s.ruleGroup(AlertRow(a), s.settings.Rules[0])
	sub := client.Subscribe(ctx, s.settings.Rules[0].Notify.Channel)
	defer func() { _ = sub.Close() }()
	if _, err := sub.ReceiveTimeout(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	counts, err := s.Counts(ctx, []string{g.Key})
	if err != nil || counts[g.Key] != 0 {
		t.Fatal(counts, err)
	}
	if err := s.ObserveAlert(ctx, a); err != nil {
		t.Fatal(err)
	}
	msg, err := sub.ReceiveTimeout(ctx, time.Second)
	if err != nil || msg.(*redis.Message).Payload != `{"bk_tenant_id":"tenant","strategy_id":"123"}` {
		t.Fatal(msg, err)
	}
	// 等级轮换在 Lifecycle 跳过旧等级的终结通知，下一次创建重复 SADD 不变更成员。
	next := a
	next.AlertID = "second"
	for _, alert := range []domain.Alert{a, next} {
		if err := s.ObserveAlert(ctx, alert); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sub.ReceiveTimeout(ctx, 20*time.Millisecond); err == nil {
		t.Fatal("replay/rotation notified")
	}
	assertOnlySets(t, s, g.Key)
	next.Status = domain.AlertStatusRecovered
	if err := s.ObserveAlert(ctx, next); err != nil {
		t.Fatal(err)
	}
	if _, err := sub.ReceiveTimeout(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := s.ObserveAlert(ctx, next); err != nil {
		t.Fatal(err)
	}
	if _, err := sub.ReceiveTimeout(ctx, 20*time.Millisecond); err == nil {
		t.Fatal("terminal replay notified")
	}
	assertOnlySets(t, s)
}

func TestActiveCacheRedisReconcileKeepsConcurrentWritesAndRepairsDifferences(t *testing.T) {
	s, client := integrationStore(t)
	ctx := t.Context()
	opening := integrationAlert("opening")
	late := integrationAlert("late")
	current := map[string]domain.Alert{opening.Fingerprint: opening, late.Fingerprint: late}
	g, _ := s.ruleGroup(AlertRow(opening), s.settings.Rules[0])
	if err := client.SAdd(ctx, g.Key, "residue").Err(); err != nil {
		t.Fatal(err)
	}
	repairs := 0
	repair := func(ctx context.Context, row RepairCandidate) error {
		repairs++
		a, found := current[row.Fingerprint]
		if !found {
			return s.RepairMember(ctx, row, nil)
		}
		return s.RepairMember(ctx, row, &a)
	}
	reader := rowsReader{rows: []Row{AlertRow(opening)}, after: func() {
		if err := s.ObserveAlert(ctx, late); err != nil {
			t.Fatal(err)
		}
	}}
	if err := s.Reconcile(ctx, reader, 2, 4096, repair); err != nil {
		t.Fatal(err)
	}
	if members := client.SMembers(ctx, g.Key).Val(); len(members) != 2 || client.SIsMember(ctx, g.Key, "residue").Val() {
		t.Fatal("concurrent member lost or residue retained", members)
	}
	if err := s.Reconcile(ctx, rowsReader{err: errors.New("later page failed")}, 2, 4096, repair); err == nil {
		t.Fatal("scan failure swallowed")
	}
	if client.SCard(ctx, g.Key).Val() != 2 {
		t.Fatal("partial scan cleaned live members")
	}
	// Redis 全丢失后直接重建分组 Set，无辅助键。
	_ = client.Del(ctx, g.Key).Err()
	if err := s.Reconcile(ctx, rowsReader{rows: []Row{AlertRow(opening), AlertRow(late)}}, 2, 4096, repair); err != nil {
		t.Fatal(err)
	}
	if client.SCard(ctx, g.Key).Val() != 2 {
		t.Fatal("rebuild lost active")
	}
	assertOnlySets(t, s, g.Key)
	// 健康成员仅进行批量核对，不逐条实时读取或重复写入。
	repairs = 0
	queries := 0
	if err := s.Reconcile(ctx, rowsReader{rows: []Row{AlertRow(opening), AlertRow(late)}, calls: &queries}, 2, 4096, repair); err != nil {
		t.Fatal(err)
	}
	if repairs != 0 || queries != 2 {
		t.Fatal("unnecessary per-member repair", repairs, queries)
	}
	delete(current, "opening")
	if err := s.Reconcile(ctx, rowsReader{rows: []Row{AlertRow(late)}}, 2, 4096, repair); err != nil {
		t.Fatal(err)
	}
	if client.SCard(ctx, g.Key).Val() != 1 {
		t.Fatal("terminal residue retained")
	}
	s.settings.Rules = nil
	if err := s.Reconcile(ctx, rowsReader{err: errors.New("must not scan")}, 2, 4096, repair); err != nil {
		t.Fatal(err)
	}
	if client.SCard(ctx, g.Key).Val() != 1 {
		t.Fatal("removed rules should stop maintaining old groups")
	}
}

func TestActiveCacheRedisMultipleRulesAndIsolation(t *testing.T) {
	s, client := integrationStore(t)
	ctx := t.Context()
	s.settings.Rules = append(s.settings.Rules, config.ActiveAlertLimitRule{Name: "source-total", MaxActive: 10, KeyPrefix: s.namespace + ":rule:source-total"}, config.ActiveAlertLimitRule{Name: "joint", GroupBy: []string{"labels.strategy_id", "subject_id"}, MaxActive: 10, KeyPrefix: s.namespace + ":rule:joint"})
	a := integrationAlert("same")
	a.SubjectID = "a:b"
	other := a
	other.BKTenantID = "other"
	for _, alert := range []domain.Alert{a, other} {
		if err := s.ObserveAlert(ctx, alert); err != nil {
			t.Fatal(err)
		}
	}
	missing := integrationAlert("missing")
	missing.Labels = nil
	if err := s.ObserveAlert(ctx, missing); err != nil {
		t.Fatal(err)
	}
	expected := []string{}
	for _, alert := range []domain.Alert{a, other} {
		for _, rule := range s.settings.Rules {
			key, _ := s.groupKey(AlertRow(alert), rule)
			expected = append(expected, key)
		}
	}
	assertOnlySets(t, s, expected...)
	if n := client.SCard(ctx, s.settings.Rules[1].KeyPrefix+":tenant").Val(); n != 2 {
		t.Fatal("source-total omitted ungrouped alert", n)
	}
	s.settings.Rules[0].MaxActive = 1
	c := NewChecker(s)
	e := checkEvent("same")
	e.SubjectID = a.SubjectID
	if c.CheckBatch(ctx, []domain.Event{e})[0] != nil {
		t.Fatal("active blocked")
	}
	e.Fingerprint = "new"
	if c.CheckBatch(ctx, []domain.Event{e})[0] == nil {
		t.Fatal("new not limited")
	}
	e.BKTenantID = "third"
	if c.CheckBatch(ctx, []domain.Event{e})[0] != nil {
		t.Fatal("tenant quota leaked")
	}
	// 不同来源相同租户、规则、fingerprint 使用独立默认前缀。
	source := checkSource()
	source.EventSourceID = "another"
	otherStore := New(client, "d", source, nil)
	defer func() {
		keys, _ := client.Keys(ctx, otherStore.namespace+"*").Result()
		if len(keys) > 0 {
			_ = client.Del(ctx, keys...).Err()
		}
	}()
	if NewChecker(otherStore).CheckBatch(ctx, []domain.Event{e})[0] != nil {
		t.Fatal("source quota leaked")
	}
	// 联合分组及带冒号值的残留也能由当前规则恢复并清理。
	key, _ := s.groupKey(AlertRow(a), s.settings.Rules[2])
	_ = client.SAdd(ctx, key, "stale").Err()
	if err := s.Reconcile(ctx, rowsReader{rows: []Row{AlertRow(a), AlertRow(other), AlertRow(missing)}}, 2, 4096, func(ctx context.Context, row RepairCandidate) error { return s.RepairMember(ctx, row, nil) }); err != nil {
		t.Fatal(err)
	}
	if client.SIsMember(ctx, key, "stale").Val() {
		t.Fatal("joint group residue retained")
	}
}

func TestActiveCacheRedisTimeoutAllowsAndCancels(t *testing.T) {
	s, client := integrationStore(t)
	ctx := t.Context()
	s.settings.TimeoutMilliseconds = 5
	// 延迟读命令，验证总访问预算生效且不重试。
	delay := &delayedCommands{delay: 25 * time.Millisecond}
	client.AddHook(delay)
	started := time.Now()
	if NewChecker(s).CheckBatch(ctx, []domain.Event{checkEvent("new")})[0] != nil {
		t.Fatal("timeout blocked")
	}
	if time.Since(started) > time.Second || delay.calls.Load() != 1 {
		t.Fatal("timeout retried or failed to return")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if NewChecker(s).CheckBatch(cancelled, []domain.Event{checkEvent("new")})[0] != nil {
		t.Fatal("cancel blocked")
	}
}

type delayedCommands struct {
	delay time.Duration
	calls atomic.Int64
}

func (h *delayedCommands) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *delayedCommands) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }

func (h *delayedCommands) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.calls.Add(1)
		timer := time.NewTimer(h.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return next(ctx, cmds)
		}
	}
}

type commandCounter struct{ commands atomic.Int64 }

func (h *commandCounter) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *commandCounter) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { h.commands.Add(1); return next(ctx, cmd) }
}

func (h *commandCounter) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.commands.Add(int64(len(cmds)))
		return next(ctx, cmds)
	}
}

func BenchmarkRedisChecker(b *testing.B) {
	for _, mode := range []string{"disabled", "below", "full", "existing"} {
		b.Run(mode, func(b *testing.B) {
			s, client := integrationStore(b)
			counter := &commandCounter{}
			client.AddHook(counter)
			a := integrationAlert("active")
			if err := s.ObserveAlert(b.Context(), a); err != nil {
				b.Fatal(err)
			}
			if mode == "disabled" {
				v := false
				s.settings.Enabled = &v
			}
			if mode == "full" || mode == "existing" {
				s.settings.Rules[0].MaxActive = 1
			}
			c := NewChecker(s)
			events := make([]domain.Event, 128)
			for i := range events {
				events[i] = checkEvent(fmt.Sprint(i))
				if mode == "existing" {
					events[i].Fingerprint = "active"
				}
			}
			c.CheckBatch(b.Context(), events)
			counter.commands.Store(0)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				c.CheckBatch(b.Context(), events)
			}
			b.StopTimer()
			b.ReportMetric(float64(counter.commands.Load())/float64(b.N*len(events)), "redis_commands/event")
		})
	}
}
