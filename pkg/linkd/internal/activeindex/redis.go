// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package activeindex

import (
	"context"
	"fmt"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/domain"
)

// Store 只维护限流规则的分组 Set；不登记独立身份、数量或重建状态。
type Store struct {
	client    redis.UniversalClient
	namespace string
	source    string
	settings  config.ActiveAlertLimits
	observer  Observer
	logger    cacheLogger
}

// New 创建不连接 Redis 的来源缓存，规则省略前缀时使用部署、来源和规则作用域。
func New(client redis.UniversalClient, deployment string, source config.EventSource, observer Observer, loggers ...cacheLogger) *Store {
	settings := config.ActiveAlertLimits{}.WithDefaults()
	if source.ActiveAlertLimits != nil {
		settings = source.ActiveAlertLimits.WithDefaults()
	}
	var logger cacheLogger
	if len(loggers) > 0 {
		logger = loggers[0]
	}
	namespace := "linkd:active-alerts:" + encode(deployment) + ":" + source.EventSourceID
	for i := range settings.Rules {
		if settings.Rules[i].KeyPrefix == "" {
			settings.Rules[i].KeyPrefix = namespace + ":rule:" + settings.Rules[i].Name
		}
	}
	return &Store{client: client, namespace: namespace, source: source.EventSourceID, settings: settings, observer: observer, logger: logger}
}

func (s *Store) observe(ctx context.Context, operation, outcome string, n int) {
	if s.observer != nil {
		s.observer.ObserveActiveAlertCache(ctx, operation, outcome, n)
	}
}

// ObserveAlert 在已有 fingerprint lease 内观察真实创建/终结，未命中分组时不写任何缓存。
// Set 自身保证重复增删无副作用；失败由周期修复处理，不回滚持久化事实。
func (s *Store) ObserveAlert(ctx context.Context, a domain.Alert) error {
	groups := []group{}
	row := AlertRow(a)
	for _, rule := range s.settings.Rules {
		if g, ok := s.ruleGroup(row, rule); ok {
			groups = append(groups, g)
		}
	}
	if len(groups) == 0 {
		return nil
	}
	call, cancel := context.WithTimeout(ctx, time.Duration(s.settings.TimeoutMilliseconds)*time.Millisecond)
	defer cancel()
	pipe := s.client.Pipeline()
	for _, g := range groups {
		s.changeMember(call, pipe, g, a.Fingerprint, a.Status == domain.AlertStatusActive)
	}
	if _, err := pipe.Exec(call); err != nil {
		s.observe(ctx, "update", "failed", 1)
		return fmt.Errorf("update active alert groups: %w", err)
	}
	s.observe(ctx, "update", "succeeded", 1)
	return nil
}

const changeMemberScript = `local n=redis.call(ARGV[1],KEYS[1],ARGV[2]);if n==1 and ARGV[3]~='' then redis.call('PUBLISH',ARGV[3],ARGV[4]) end;return n`

func (s *Store) changeMember(ctx context.Context, pipe redis.Pipeliner, g group, fingerprint string, active bool) {
	if g.Channel == "" {
		if active {
			pipe.SAdd(ctx, g.Key, fingerprint)
		} else {
			pipe.SRem(ctx, g.Key, fingerprint)
		}
		return
	}
	operation := "SREM"
	if active {
		operation = "SADD"
	}
	pipe.Eval(ctx, changeMemberScript, []string{g.Key}, operation, fingerprint, g.Channel, g.Message)
}

// Counts 通过分组 Set 的 SCARD 批量获取近似数量；空集合自然为零，不需要就绪标记。
func (s *Store) Counts(ctx context.Context, keys []string) (map[string]int64, error) {
	pipe := s.client.Pipeline()
	commands := make(map[string]*redis.IntCmd, len(keys))
	for _, key := range keys {
		commands[key] = pipe.SCard(ctx, key)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(keys))
	for key, command := range commands {
		value, err := command.Result()
		if err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, nil
}

// Members 只在规则满额时批量读取对应分组的 fingerprint 成员关系。
func (s *Store) Members(ctx context.Context, groups map[string][]string) (map[string][]bool, error) {
	pipe := s.client.Pipeline()
	commands := make(map[string]*redis.BoolSliceCmd, len(groups))
	for key, fps := range groups {
		members := make([]any, len(fps))
		for i, fp := range fps {
			members[i] = fp
		}
		commands[key] = pipe.SMIsMember(ctx, key, members...)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	out := make(map[string][]bool, len(groups))
	for key, command := range commands {
		values, err := command.Result()
		if err != nil {
			return nil, err
		}
		out[key] = values
	}
	return out, nil
}

// RepairMember 在调用方复用的 fingerprint lease 内按实时活动状态修复一个分组。
// 规则变化或旧生命周期残留不增加身份登记；由当前字段计算成员归属。
func (s *Store) RepairMember(ctx context.Context, candidate RepairCandidate, active *domain.Alert) error {
	keep := false
	if active != nil && active.Status == domain.AlertStatusActive {
		if err := s.ObserveAlert(ctx, *active); err != nil {
			return err
		}
		for _, rule := range s.settings.Rules {
			if key, ok := s.groupKey(AlertRow(*active), rule); ok && key == candidate.GroupKey {
				keep = true
				break
			}
		}
	}
	if keep {
		return nil
	}
	call, cancel := context.WithTimeout(ctx, time.Duration(s.settings.TimeoutMilliseconds)*time.Millisecond)
	defer cancel()
	if candidate.NotifyChannel == "" {
		return s.client.SRem(call, candidate.GroupKey, candidate.Fingerprint).Err()
	}
	return s.client.Eval(call, changeMemberScript, []string{candidate.GroupKey}, "SREM", candidate.Fingerprint, candidate.NotifyChannel, candidate.NotifyMessage).Err()
}

type cacheLogger interface {
	WarnContext(context.Context, string, ...any)
}

func (s *Store) readFailure(ctx context.Context, err error) {
	if s.logger != nil && ctx.Err() == nil {
		s.logger.WarnContext(ctx, "active alert limit cache unavailable", "module", "cleaner", "operation", "active_alert_limit_check", "error", err, "event_source_id", s.source)
	}
}
