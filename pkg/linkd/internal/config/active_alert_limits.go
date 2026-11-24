// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// ActiveAlertLimits 配置来源活跃集合与软限流；nil 表示不维护缓存。
// Enabled 仅控制拦截，关闭后仍维护 Rules 的集合及通知。
type ActiveAlertLimits struct {
	// Enabled 省略时启用；false 仍维护 Rules 对应的集合与通知。
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// CountCacheTTLMilliseconds 是每个 Cleaner 来源任务的数量采样窗口，默认 1000ms。
	CountCacheTTLMilliseconds int `yaml:"count_cache_ttl_milliseconds,omitempty" json:"count_cache_ttl_milliseconds,omitempty"`
	// CountCacheMaxGroups 限制单个来源任务的进程内分组条目，默认 4096。
	CountCacheMaxGroups int `yaml:"count_cache_max_groups,omitempty" json:"count_cache_max_groups,omitempty"`
	// TimeoutMilliseconds 限制主流程缓存 I/O，默认 100ms，失败不增加应用层重试。
	TimeoutMilliseconds int `yaml:"timeout_milliseconds,omitempty" json:"timeout_milliseconds,omitempty"`
	// Rules 同时声明需要维护的活跃集合；空列表不维护索引。
	Rules []ActiveAlertLimitRule `yaml:"rules" json:"rules"`
}

// ActiveAlertLimitRule 以 opening Event 的稳定字段分组；空 GroupBy 限制来源总量。
type ActiveAlertLimitRule struct {
	// Name 是来源内稳定规则身份，用于默认集合前缀和限流诊断。
	Name string `yaml:"name" json:"name"`
	// GroupBy 只读取 opening Event 原始字段，不应用 Enrich 补丁。
	GroupBy []string `yaml:"group_by" json:"group_by"`
	// MaxActive 是近似活跃数量上限，必须为正整数。
	MaxActive int64 `yaml:"max_active" json:"max_active"`
	// KeyPrefix 可显式保持外部协议；省略时派生部署、来源和规则作用域。
	KeyPrefix string `yaml:"key_prefix,omitempty" json:"key_prefix,omitempty"`
	// Notify 省略时不发送通知，配置后仅在成员增删时发布。
	Notify *ActiveAlertLimitNotify `yaml:"notify,omitempty" json:"notify,omitempty"`
}

// ActiveAlertLimitNotify 只映射租户、来源和分组字段，所有输出值均为字符串。
type ActiveAlertLimitNotify struct {
	// Channel 是该规则成员变化通知的固定频道。
	Channel string `yaml:"channel" json:"channel"`
	// Fields 将通知字段名映射到租户、来源或本规则分组路径。
	Fields map[string]string `yaml:"fields" json:"fields"`
}

// WithDefaults 返回隔离副本，只补通用运行预算，不注入任何来源的业务规则。
func (c ActiveAlertLimits) WithDefaults() ActiveAlertLimits {
	c.Rules = slices.Clone(c.Rules)
	if c.Enabled != nil {
		v := *c.Enabled
		c.Enabled = &v
	}
	if c.CountCacheTTLMilliseconds == 0 {
		c.CountCacheTTLMilliseconds = 1000
	}
	if c.CountCacheMaxGroups == 0 {
		c.CountCacheMaxGroups = 4096
	}
	if c.TimeoutMilliseconds == 0 {
		c.TimeoutMilliseconds = 100
	}
	for i := range c.Rules {
		c.Rules[i].GroupBy = slices.Clone(c.Rules[i].GroupBy)
		if n := c.Rules[i].Notify; n != nil {
			v := *n
			v.Fields = maps.Clone(n.Fields)
			c.Rules[i].Notify = &v
		}
	}
	return c
}

// IsEnabled 返回软限流开关；省略时启用，空规则始终不产生访问。
func (c ActiveAlertLimits) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// Validate 在配置发布边界检查可执行字段与运行预算。
func (c ActiveAlertLimits) Validate() error {
	c = c.WithDefaults()
	if c.CountCacheTTLMilliseconds < 1 || c.CountCacheMaxGroups < 1 || c.TimeoutMilliseconds < 1 || c.TimeoutMilliseconds > 60000 {
		return fmt.Errorf("active_alert_limits requires positive cache budgets and timeout between 1 and 60000ms")
	}
	names := map[string]bool{}
	for _, r := range c.Rules {
		if !hookNamePattern.MatchString(r.Name) || names[r.Name] || r.MaxActive < 1 {
			return fmt.Errorf("active_alert_limits rules require unique names and positive max_active")
		}
		names[r.Name] = true
		fields := map[string]bool{"bk_tenant_id": true, "event_source_id": true}
		for _, field := range r.GroupBy {
			if !activeLimitField(field) || fields[field] {
				return fmt.Errorf("invalid or duplicate active_alert_limits group_by field %q", field)
			}
			fields[field] = true
		}
		if r.KeyPrefix != "" && strings.TrimSpace(r.KeyPrefix) != r.KeyPrefix {
			return fmt.Errorf("active_alert_limits key_prefix contains surrounding whitespace")
		}
		if n := r.Notify; n != nil {
			if n.Channel == "" || len(n.Fields) == 0 {
				return fmt.Errorf("active_alert_limits notify requires channel and fields")
			}
			for name, field := range n.Fields {
				if name == "" || !fields[field] {
					return fmt.Errorf("active_alert_limits notify field %q is not a grouping or identity field", field)
				}
			}
		}
	}
	return nil
}

func activeLimitField(field string) bool {
	if field == "subject_system" || field == "subject_type" || field == "subject_id" {
		return true
	}
	for _, prefix := range []string{"labels.", "dimensions."} {
		if strings.HasPrefix(field, prefix) {
			return len(field) > len(prefix)
		}
	}
	return false
}
