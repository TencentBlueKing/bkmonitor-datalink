// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package activeindex

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/store/storetest"
)

type checkBackend struct {
	mu                      sync.Mutex
	countCalls, activeCalls int
	count                   int64
	active                  bool
	err                     error
	keys                    []string
}

func (f *checkBackend) Counts(_ context.Context, keys []string) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.countCalls++
	f.keys = append([]string(nil), keys...)
	out := map[string]int64{}
	for _, key := range keys {
		out[key] = f.count
	}
	return out, f.err
}

func (f *checkBackend) Members(_ context.Context, groups map[string][]string) (map[string][]bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activeCalls++
	out := map[string][]bool{}
	for key, fps := range groups {
		out[key] = make([]bool, len(fps))
		for i := range fps {
			out[key][i] = f.active
		}
	}
	return out, f.err
}

func checkSource() config.EventSource {
	return config.EventSource{EventSourceID: "source", ActiveAlertLimits: &config.ActiveAlertLimits{Rules: []config.ActiveAlertLimitRule{{Name: "strategy", GroupBy: []string{"labels.strategy_id"}, MaxActive: 2}}}}
}

func checkEvent(id string) domain.Event {
	e := storetest.Event("tenant", id, id, "warning")
	e.EventSourceID = "source"
	e.Labels = domain.DimensionMap{"strategy_id": domain.NewStringScalar("123")}
	return e
}

func TestCheckerBatchCacheAndSoftLimit(t *testing.T) {
	s := New(nil, "deploy", checkSource(), nil)
	c := NewChecker(s)
	b := &checkBackend{count: 1}
	c.backend = b
	events := []domain.Event{checkEvent("a"), checkEvent("b"), checkEvent("c")}
	for range 3 {
		for _, v := range c.CheckBatch(t.Context(), events) {
			if v != nil {
				t.Fatal("unexpected rejection")
			}
		}
	}
	if b.countCalls != 1 || len(b.keys) != 1 || b.activeCalls != 0 {
		t.Fatal("per-event I/O", b)
	}
	c.now = func() time.Time { return time.Now().Add(2 * time.Second) }
	b.count = 2
	got := c.CheckBatch(t.Context(), events)
	for _, v := range got {
		if v == nil || v.MaxActive != 2 || v.Count != 2 {
			t.Fatal("lost diagnosis", v)
		}
	}
	if b.countCalls != 2 || b.activeCalls != 1 {
		t.Fatal(b)
	}
	b.active = true
	if c.CheckBatch(t.Context(), events)[0] != nil {
		t.Fatal("existing active blocked")
	}
	b.err = errors.New("redis offline")
	if c.CheckBatch(t.Context(), events)[0] != nil {
		t.Fatal("cache failure blocked")
	}
}

func TestCheckerBypassesTerminalMissingAndDisabled(t *testing.T) {
	for _, mode := range []string{"resolved", "mixed", "missing", "disabled", "unconfigured", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			source := checkSource()
			e := checkEvent("one")
			switch mode {
			case "resolved":
				e.Evaluations[0].Action = domain.EventActionResolved
			case "mixed":
				e.Evaluations = append(e.Evaluations, domain.EventEvaluation{Severity: "critical", Action: domain.EventActionClosed})
			case "missing":
				e.Labels = nil
			case "disabled":
				v := false
				source.ActiveAlertLimits.Enabled = &v
			case "unconfigured":
				source.ActiveAlertLimits = nil
			}
			c := NewChecker(New(nil, "d", source, nil))
			b := &checkBackend{count: 10}
			c.backend = b
			ctx := t.Context()
			if mode == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				b.err = context.Canceled
			}
			if c.CheckBatch(ctx, []domain.Event{e})[0] != nil {
				t.Fatal("must pass")
			}
			if mode != "cancelled" && b.countCalls != 0 {
				t.Fatal("unnecessary Redis access")
			}
		})
	}
}

func TestRuleGroupingAndNotify(t *testing.T) {
	source := checkSource()
	source.ActiveAlertLimits.Rules[0].KeyPrefix = "alarmd:open_alerts"
	source.ActiveAlertLimits.Rules[0].Notify = &config.ActiveAlertLimitNotify{Channel: "changes", Fields: map[string]string{"strategy_id": "labels.strategy_id", "bk_tenant_id": "bk_tenant_id"}}
	s := New(nil, "d", source, nil)
	row := EventRow(checkEvent("one"))
	g, ok := s.ruleGroup(row, s.settings.Rules[0])
	if !ok || g.Key != "alarmd:open_alerts:tenant:123" || g.Message != `{"bk_tenant_id":"tenant","strategy_id":"123"}` {
		t.Fatal(g)
	}
	row.Labels["strategy_id"], _ = domain.NewNumberScalar(123)
	numeric, _ := s.ruleGroup(row, s.settings.Rules[0])
	if numeric.Key != g.Key {
		t.Fatal("number/string split")
	}
	rule := config.ActiveAlertLimitRule{Name: "joint", GroupBy: []string{"labels.strategy_id", "subject_id"}}
	row.SubjectID = "a:b"
	joint, _ := s.ruleGroup(row, rule)
	if joint.Key == g.Key {
		t.Fatal("joint identity collided")
	}
	other := source
	other.EventSourceID = "other"
	other.ActiveAlertLimits = &config.ActiveAlertLimits{Rules: []config.ActiveAlertLimitRule{{Name: "strategy", GroupBy: []string{"labels.strategy_id"}, MaxActive: 2}}}
	otherGroup, _ := New(nil, "d", other, nil).ruleGroup(row, rule)
	if otherGroup.Key == joint.Key {
		t.Fatal("source isolation lost")
	}
}

func TestCheckerConcurrentBoundedCache(t *testing.T) {
	source := checkSource()
	source.ActiveAlertLimits.CountCacheMaxGroups = 4
	c := NewChecker(New(nil, "d", source, nil))
	c.backend = &checkBackend{}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			e := checkEvent(fmt.Sprint(i))
			e.Labels["strategy_id"] = domain.NewStringScalar(fmt.Sprint(i))
			c.CheckBatch(t.Context(), []domain.Event{e})
		})
	}
	wg.Wait()
	if len(c.counts) > 4 {
		t.Fatal("cache budget exceeded")
	}
}

func BenchmarkChecker(b *testing.B) {
	for _, mode := range []string{"disabled", "below", "full", "existing"} {
		b.Run(mode, func(b *testing.B) {
			source := checkSource()
			backend := &checkBackend{}
			switch mode {
			case "disabled":
				v := false
				source.ActiveAlertLimits.Enabled = &v
			case "full":
				backend.count = 2
			case "existing":
				backend.count = 2
				backend.active = true
			}
			c := NewChecker(New(nil, "d", source, nil))
			c.backend = backend
			events := make([]domain.Event, 128)
			for i := range events {
				events[i] = checkEvent(fmt.Sprint(i))
			}
			c.CheckBatch(b.Context(), events)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				c.CheckBatch(b.Context(), events)
			}
			b.StopTimer()
			b.ReportMetric(float64(backend.countCalls+backend.activeCalls)/float64(b.N*len(events)), "cache_batches/event")
		})
	}
}

// 每条满额规则都使用自身 Set，其他规则已有成员不能绕过当前规则。
func TestCheckerChecksEachFullGroupAndDeduplicates(t *testing.T) {
	source := checkSource()
	source.ActiveAlertLimits.Rules = append(source.ActiveAlertLimits.Rules, config.ActiveAlertLimitRule{Name: "source-total", MaxActive: 1})
	s := New(nil, "d", source, nil)
	c := NewChecker(s)
	b := &groupBackend{checkBackend: checkBackend{count: 3}, requests: map[string][]string{}}
	b.allowed = s.settings.Rules[1].KeyPrefix + ":tenant"
	c.backend = b
	events := []domain.Event{checkEvent("same"), checkEvent("same")}
	for _, v := range c.CheckBatch(t.Context(), events) {
		if v == nil || v.Rule != "strategy" {
			t.Fatal(v)
		}
	}
	if len(b.requests) != 2 {
		t.Fatal(b.requests)
	}
	for _, fps := range b.requests {
		if len(fps) != 1 {
			t.Fatal("duplicate fingerprint lookup", fps)
		}
	}
}

type groupBackend struct {
	checkBackend
	allowed  string
	requests map[string][]string
}

func (b *groupBackend) Members(_ context.Context, groups map[string][]string) (map[string][]bool, error) {
	b.requests = groups
	out := map[string][]bool{}
	for key, fps := range groups {
		out[key] = make([]bool, len(fps))
		for i := range fps {
			out[key][i] = key == b.allowed
		}
	}
	return out, nil
}
