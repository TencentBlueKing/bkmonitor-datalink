// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package activeindex

import (
	"context"
	"sync"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

type countBackend interface {
	Counts(context.Context, []string) (map[string]int64, error)
	Members(context.Context, map[string][]string) (map[string][]bool, error)
}

type cachedCount struct {
	value int64
	until time.Time
}

// Checker 只读缓存并允许采样超量，不申请额度，也不在失败时重试。
// 多个 lane 共享有界数量缓存，Redis I/O 不持有进程内锁。
type Checker struct {
	store   *Store
	backend countBackend
	mu      sync.Mutex
	counts  map[string]cachedCount
	now     func() time.Time
}

// NewChecker 创建一个来源任务独享的有界数量缓存。
func NewChecker(s *Store) *Checker {
	return &Checker{store: s, backend: s, counts: map[string]cachedCount{}, now: time.Now}
}

// CheckBatch 返回逐 Event 的拦截诊断；nil 表示放行。恢复/关闭及降级路径始终放行。
func (c *Checker) CheckBatch(ctx context.Context, events []domain.Event) []*store.ActiveAlertLimitDecision {
	out := make([]*store.ActiveAlertLimitDecision, len(events))
	s := c.store
	if !s.settings.IsEnabled() || len(s.settings.Rules) == 0 {
		return out
	}
	type candidate struct {
		index, rule int
		key         string
	}
	candidates := make([]candidate, 0, len(events)*len(s.settings.Rules))
	missing := map[string]bool{}
	values := map[string]int64{}
	now := c.now()
	c.mu.Lock()
	for i, e := range events {
		triggerOnly := len(e.Evaluations) > 0
		for _, evaluation := range e.Evaluations {
			if evaluation.Action != domain.EventActionTriggered {
				triggerOnly = false
				break
			}
		}
		if !triggerOnly {
			continue
		}
		row := EventRow(e)
		for j, rule := range s.settings.Rules {
			key, ok := s.groupKey(row, rule)
			if !ok {
				s.observe(ctx, "check", "missing_field", 1)
				continue
			}
			candidates = append(candidates, candidate{index: i, rule: j, key: key})
			if v, ok := c.counts[key]; ok && now.Before(v.until) {
				values[key] = v.value
			} else {
				missing[key] = true
			}
		}
	}
	c.mu.Unlock()
	call, cancel := context.WithTimeout(ctx, time.Duration(s.settings.TimeoutMilliseconds)*time.Millisecond)
	defer cancel()
	if len(missing) > 0 {
		keys := make([]string, 0, len(missing))
		for k := range missing {
			keys = append(keys, k)
		}
		fresh, err := c.backend.Counts(call, keys)
		if err != nil {
			s.readFailure(ctx, err)
			s.observe(ctx, "check", "failed_open", len(events))
			return out
		}
		c.mu.Lock()
		for k, v := range fresh {
			if len(c.counts) >= s.settings.CountCacheMaxGroups {
				for old := range c.counts {
					delete(c.counts, old)
					break
				}
			}
			c.counts[k] = cachedCount{v, now.Add(time.Duration(s.settings.CountCacheTTLMilliseconds) * time.Millisecond)}
			values[k] = v
		}
		c.mu.Unlock()
	}

	// 每个满额分组只查询一次，组内重复 fingerprint 也只传输一次。
	type membership struct{ key, fp string }
	positions := map[membership]int{}
	requests := map[string][]string{}
	for _, v := range candidates {
		if values[v.key] < s.settings.Rules[v.rule].MaxActive {
			continue
		}
		id := membership{v.key, events[v.index].Fingerprint}
		if _, ok := positions[id]; !ok {
			positions[id] = len(requests[v.key])
			requests[v.key] = append(requests[v.key], id.fp)
		}
	}
	if len(requests) == 0 {
		return out
	}
	members, err := c.backend.Members(call, requests)
	if err != nil {
		s.readFailure(ctx, err)
		s.observe(ctx, "check", "failed_open", len(events))
		return out
	}
	for _, v := range candidates {
		rule := s.settings.Rules[v.rule]
		if values[v.key] < rule.MaxActive || out[v.index] != nil {
			continue
		}
		id := membership{v.key, events[v.index].Fingerprint}
		if members[v.key][positions[id]] {
			continue
		}
		out[v.index] = &store.ActiveAlertLimitDecision{Rule: rule.Name, Group: groupFields(EventRow(events[v.index]), rule), Count: values[v.key], MaxActive: rule.MaxActive}
		s.observe(ctx, "check", "limited", 1)
	}
	return out
}
