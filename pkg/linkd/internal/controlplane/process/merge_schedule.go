// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License").

package controlplaneprocess

import (
	"context"
	"slices"
	"time"

	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/store"
)

type mergeDueReader interface {
	MergeDueScores(context.Context, string, []string) ([]float64, error)
}

// mergeWaitingScan 用到期提示选择窗口，普通裁决跨页去重；到期成员和丢失提示保留补偿项。
// 一分钟补查承接依赖刷新。
// 仅保存当前等待窗口的调度时间，不复制成员或 Alert；扫描完成即剪掉已退出的窗口。
type mergeWaitingScan struct {
	due     mergeDueReader
	seen    map[string]bool
	checked map[string]time.Time
	round   int
}

func (s *mergeWaitingScan) selectWork(ctx context.Context, page store.MergeWorkPage, now time.Time) ([]store.MergeWorkItem, error) {
	if s.seen == nil {
		s.seen = map[string]bool{}
		s.checked = map[string]time.Time{}
	}
	groups := map[string][]string{}
	pageSeen := map[string]bool{}
	for _, item := range page.Items {
		if item.WindowID == "" {
			continue
		}
		tenant := item.Alert.Alert.BKTenantID
		key := tenant + ":" + item.WindowID
		if s.seen[key] || pageSeen[key] {
			continue
		}
		pageSeen[key] = true
		groups[tenant] = append(groups[tenant], item.WindowID)
	}
	ready := map[string]bool{}
	missing := map[string]bool{}
	for tenant, ids := range groups {
		for start := 0; start < len(ids); start += redisstate.MergeDueBatchSize {
			chunk := ids[start:min(start+redisstate.MergeDueBatchSize, len(ids))]
			scores, err := s.due.MergeDueScores(ctx, tenant, chunk)
			if err != nil {
				return nil, err
			}
			for i, id := range chunk {
				key := tenant + ":" + id
				ready[key] = scores[i] <= float64(now.UnixMilli())
				missing[key] = !(scores[i] > 0)
			}
		}
	}
	result := []store.MergeWorkItem{}
	for _, item := range page.Items {
		if item.WindowID == "" {
			result = append(result, item)
			continue
		}
		key := item.Alert.Alert.BKTenantID + ":" + item.WindowID
		// 到期成员也需补偿：窗口内容可能已丢失而到期提示仍在，不能按提示存在去重。
		compensate := missing[key]
		if item.Alert.Alert.Merge != nil {
			for _, wait := range item.Alert.Alert.Merge.Pending {
				if wait.WindowID == item.WindowID && !now.Before(wait.Deadline) {
					compensate = true
					break
				}
			}
		}
		if compensate {
			result = append(result, item)
			continue
		}
		if s.seen[key] {
			continue
		}
		if ready[key] || now.Sub(s.checked[key]) >= time.Minute {
			result = append(result, item)
			s.checked[key] = now
		}
		s.seen[key] = true
	}
	if page.Next.TenantID == "" {
		for key := range s.checked {
			if !s.seen[key] {
				delete(s.checked, key)
			}
		}
		clear(s.seen)
		// 每个窗口在一页内最多有 MergeWorkBatchSize 项，轮次循环即可覆盖全部起点。
		s.round = (s.round + 1) % store.MergeWorkBatchSize
	}
	return result, nil
}

// mergeWaitingJobs 将同窗口的成员补偿放在同一次窗口租约内，避免相互抢锁而跳过。
// 单成员失败仍执行本页其他成员；整页和每项持久事实负责下一轮重试。
func mergeWaitingJobs(items []store.MergeWorkItem, round int, check func(context.Context, []store.MergeWorkItem, time.Time) error) ([]mergeJob, error) {
	groups := map[string][]store.MergeWorkItem{}
	jobs := []mergeJob{}
	for _, item := range items {
		window := item.WindowID
		if window == "" {
			if item.Alert.Alert.MergeChange == nil {
				return nil, policy.ErrInvalid
			}
			window = item.Alert.Alert.MergeChange.WindowID
		}
		tenant := item.Alert.Alert.BKTenantID
		key := tenant + ":" + window
		if _, found := groups[key]; !found {
			jobs = append(jobs, mergeJob{tenant: tenant, window: window, run: func(ctx context.Context) error {
				return check(ctx, groups[key], time.Now().UTC())
			}})
		}
		groups[key] = append(groups[key], item)
	}
	// 多个慢成员仍可能用尽作业预算；按完整扫描轮次轮换起点，下一轮不再固定重试同一前缀。
	for key, members := range groups {
		start := round % len(members)
		if start != 0 {
			groups[key] = slices.Concat(members[start:], members[:start])
		}
	}
	return jobs, nil
}
