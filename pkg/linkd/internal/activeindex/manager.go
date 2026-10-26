// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package activeindex

import (
	"context"
	"encoding/json"
	"strings"

	"linkd/internal/config"
)

// Reconcile 分页补齐缺失成员，再分批核对当前规则的 Set 并清理残留。
// 不建立来源快照或临时 Redis 索引；repair 必须在已有 fingerprint lease 内重读当前状态。
// 存储读取失败时停止本轮，不能把失败解释为空结果并删除成员。
func (s *Store) Reconcile(ctx context.Context, reader Reader, pageSize, maxBytes int, repair func(context.Context, RepairCandidate) error) error {
	if len(s.settings.Rules) == 0 {
		return nil
	}
	fields := []string{}
	unique := map[string]bool{}
	for _, rule := range s.settings.Rules {
		for _, f := range rule.GroupBy {
			if !unique[f] {
				unique[f] = true
				fields = append(fields, f)
			}
		}
	}
	q := Query{Sources: []string{s.source}, Fields: fields, PageSize: pageSize, MaxBytes: maxBytes}
	err := reader.ScanActiveIndex(ctx, q, func(rows []Row) error {
		requests := map[string][]string{}
		candidates := map[string][]RepairCandidate{}
		for _, row := range rows {
			for _, rule := range s.settings.Rules {
				if g, ok := s.ruleGroup(row, rule); ok {
					requests[g.Key] = append(requests[g.Key], row.Fingerprint)
					candidates[g.Key] = append(candidates[g.Key], RepairCandidate{Row: row, GroupKey: g.Key, NotifyChannel: g.Channel, NotifyMessage: g.Message})
				}
			}
		}
		if len(requests) == 0 {
			return nil
		}
		found, err := s.Members(ctx, requests)
		if err != nil {
			return err
		}
		for key, items := range candidates {
			for i, candidate := range items {
				if !found[key][i] {
					if err := repair(ctx, candidate); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, rule := range s.settings.Rules {
		// 前缀由本来源规则独占；转义 Redis glob，避免配置中的字符被解释成通配符。
		pattern := strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`).Replace(rule.KeyPrefix) + ":*"
		var cursor uint64
		for {
			keys, next, err := s.client.ScanType(ctx, cursor, pattern, int64(pageSize), "set").Result()
			if err != nil {
				return err
			}
			for _, key := range keys {
				candidate, ok := s.groupCandidate(key, rule)
				if !ok {
					continue
				}
				var membersCursor uint64
				for {
					fps, next, err := s.client.SScan(ctx, key, membersCursor, "", int64(pageSize)).Result()
					if err != nil {
						return err
					}
					// SCAN COUNT 是提示，紧凑 Set 可能一次返回全部成员；存储查询仍按配置预算切批。
					for start := 0; start < len(fps); start += pageSize {
						end := min(start+pageSize, len(fps))
						batch := fps[start:end]
						query := q
						query.BKTenantID = candidate.BKTenantID
						query.Fingerprints = batch
						current := make(map[string]Row, len(batch))
						if err := reader.ScanActiveIndex(ctx, query, func(rows []Row) error {
							for _, row := range rows {
								current[row.Fingerprint] = row
							}
							return nil
						}); err != nil {
							return err
						}
						for _, fp := range batch {
							row, exists := current[fp]
							if exists {
								if expected, ok := s.groupKey(row, rule); ok && expected == key {
									continue
								}
							}
							item := candidate
							item.Fingerprint = fp
							if exists {
								item.AlertID = row.AlertID
							}
							if err := repair(ctx, item); err != nil {
								return err
							}
						}
					}
					membersCursor = next
					if membersCursor == 0 {
						break
					}
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	s.observe(ctx, "reconcile", "succeeded", 1)
	return nil
}

// groupCandidate 从当前规则的公开集合布局恢复通知参数，不保存额外反向索引。
func (s *Store) groupCandidate(key string, rule config.ActiveAlertLimitRule) (RepairCandidate, bool) {
	suffix, ok := strings.CutPrefix(key, rule.KeyPrefix+":")
	if !ok {
		return RepairCandidate{}, false
	}
	tenant, tail, hasGroup := strings.Cut(suffix, ":")
	values := []string{}
	switch len(rule.GroupBy) {
	case 0:
		if hasGroup {
			return RepairCandidate{}, false
		}
	case 1:
		if !hasGroup {
			return RepairCandidate{}, false
		}
		values = append(values, tail)
	default:
		if !hasGroup || json.Unmarshal([]byte(tail), &values) != nil || len(values) != len(rule.GroupBy) {
			return RepairCandidate{}, false
		}
	}
	item := RepairCandidate{Row: Row{BKTenantID: tenant, EventSourceID: s.source}, GroupKey: key}
	if rule.Notify != nil {
		fields := map[string]string{"bk_tenant_id": tenant, "event_source_id": s.source}
		for i, path := range rule.GroupBy {
			fields[path] = values[i]
		}
		message := map[string]string{}
		for name, path := range rule.Notify.Fields {
			message[name] = fields[path]
		}
		encoded, _ := json.Marshal(message)
		item.NotifyChannel = rule.Notify.Channel
		item.NotifyMessage = string(encoded)
	}
	return item, true
}
