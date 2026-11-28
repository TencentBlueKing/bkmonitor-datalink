// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"linkd/internal/domain"
)

// CollectorConfig 配置控制面接收服务与独立 Python 执行器的同步连接。
type CollectorConfig struct {
	// CompletedRetentionSeconds 是终结请求的输入/结果保留期；零值使用七天，不作用于待处理请求和来源游标。
	CompletedRetentionSeconds int64 `yaml:"completed_retention_seconds"`

	// MaxConcurrentRequests 限制控制面同时处理的推送请求，零值使用 4；满载立即拒绝。
	MaxConcurrentRequests int `yaml:"max_concurrent_requests"`
	// RecoveryBatchSize 限制每批恢复的请求身份数量，零值使用 32。
	RecoveryBatchSize int64  `yaml:"recovery_batch_size"`
	Endpoint          string `yaml:"endpoint"`
	Token             string `yaml:"token"`
	TimeoutSeconds    int    `yaml:"timeout_seconds"`
	MaxRequestBytes   int64  `yaml:"max_request_bytes"`
	MaxResponseBytes  int64  `yaml:"max_response_bytes"`
}

func (c CollectorConfig) validate() error {
	if c.CompletedRetentionSeconds < 0 || c.CompletedRetentionSeconds > int64((1<<63-1)/time.Second) {
		return fmt.Errorf("collector.completed_retention_seconds must fit a nonnegative duration")
	}

	if c.MaxConcurrentRequests < 0 {
		return fmt.Errorf("collector.max_concurrent_requests cannot be negative")
	}
	if c.RecoveryBatchSize < 0 {
		return fmt.Errorf("collector.recovery_batch_size cannot be negative")
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("collector.endpoint must be an HTTP URL")
	}
	if c.Token == "" || c.Token == redactedSecret {
		return fmt.Errorf("collector.token is required")
	}
	if c.TimeoutSeconds <= 0 || c.MaxRequestBytes <= 0 || c.MaxResponseBytes <= 0 {
		return fmt.Errorf("collector requires positive timeout and byte budgets")
	}
	return nil
}

// SourceCollection 描述来源插件执行配置，Configs 可能含第三方凭据，普通查询全部脱敏。
type SourceCollection struct {
	// TenantBindings 为共享 push 接入提供各租户独立凭据和完整插件配置；不与根级 Secret/Configs 合并。
	TenantBindings []CollectionTenantBinding `json:"tenant_bindings,omitempty" yaml:"tenant_bindings,omitempty"`

	// SourceName 保留插件的展示名称，省略时使用来源 ID。
	SourceName string `json:"source_name,omitempty" yaml:"source_name,omitempty"`
	// Timezone 用于解析来源无时区时间；省略时沿用运行时默认 Asia/Shanghai。
	Timezone string `json:"timezone,omitempty" yaml:"timezone,omitempty"`
	// InitialState 只在来源尚无已提交游标时使用，不覆盖运行中进度。
	InitialState map[string]any `json:"initial_state,omitempty" yaml:"initial_state,omitempty"`
	// PluginSourceID 保留迁入插件原来源身份，用于既有指纹算法；查询/存储仍使用 EventSourceID。
	PluginSourceID string `json:"plugin_source_id,omitempty" yaml:"plugin_source_id,omitempty"`
	// RecoveryIntervalMinutes 为零时关闭独立恢复周期。
	RecoveryIntervalMinutes int            `json:"recovery_interval_minutes,omitempty" yaml:"recovery_interval_minutes,omitempty"`
	Plugin                  string         `json:"plugin" yaml:"plugin"`
	Mode                    string         `json:"mode" yaml:"mode"`
	Secret                  string         `json:"secret" yaml:"secret"`
	Configs                 map[string]any `json:"configs,omitempty" yaml:"configs,omitempty"`
	IntervalMinutes         int            `json:"interval_minutes,omitempty" yaml:"interval_minutes,omitempty"`
}

// CollectionTenantBinding 由管理配置确定接入租户，不能从请求正文推导。
type CollectionTenantBinding struct {
	TenantID string         `json:"bk_tenant_id" yaml:"bk_tenant_id"`
	Enabled  bool           `json:"enabled" yaml:"enabled"`
	Secret   string         `json:"secret" yaml:"secret"`
	Configs  map[string]any `json:"configs,omitempty" yaml:"configs,omitempty"`
	// PluginSourceID 保留该租户在 KAC 中的原来源身份。
	PluginSourceID string `json:"plugin_source_id,omitempty" yaml:"plugin_source_id,omitempty"`
	SourceName     string `json:"source_name,omitempty" yaml:"source_name,omitempty"`
}

func (c SourceCollection) clone() *SourceCollection {
	c.Configs = cloneProcessorConfigMap(c.Configs)
	c.InitialState = cloneProcessorConfigMap(c.InitialState)
	c.TenantBindings = append([]CollectionTenantBinding(nil), c.TenantBindings...)
	for i := range c.TenantBindings {
		c.TenantBindings[i].Configs = cloneProcessorConfigMap(c.TenantBindings[i].Configs)
	}
	return &c
}

func (c SourceCollection) validate() error {
	if c.Timezone != "" {
		if _, err := time.LoadLocation(c.Timezone); err != nil {
			return fmt.Errorf("collection.timezone: %w", err)
		}
	}
	if c.RecoveryIntervalMinutes < 0 {
		return fmt.Errorf("collection.recovery_interval_minutes cannot be negative")
	}
	if strings.TrimSpace(c.Plugin) == "" {
		return fmt.Errorf("collection.plugin is required")
	}
	if c.Mode != "push" && c.Mode != "pull" {
		return fmt.Errorf("collection.mode must be push or pull")
	}
	if len(c.TenantBindings) > 0 {
		if c.Mode != "push" || c.RecoveryIntervalMinutes != 0 {
			return fmt.Errorf("collection.tenant_bindings supports push without scheduled recovery")
		}
		if c.Secret != "" || len(c.Configs) > 0 || len(c.InitialState) > 0 {
			return fmt.Errorf("shared collection credentials, configs and state must not be set at root")
		}
		tenants, secrets := map[string]bool{}, map[string]bool{}
		for _, binding := range c.TenantBindings {
			if err := domain.ValidateIdentityPart("collection.tenant_bindings.bk_tenant_id", binding.TenantID, 64); err != nil {
				return err
			}
			if tenants[binding.TenantID] {
				return fmt.Errorf("duplicate collection tenant binding")
			}
			tenants[binding.TenantID] = true
			if binding.Secret == "" || binding.Secret == redactedSecret {
				return fmt.Errorf("collection tenant secret is required")
			}
			// 同一个 Secret 只能决定一个租户，否则接收身份取决于配置排列顺序。
			if secrets[binding.Secret] {
				return fmt.Errorf("collection tenant secrets must be distinct")
			}
			secrets[binding.Secret] = true
			for _, value := range binding.Configs {
				if value == redactedSecret {
					return fmt.Errorf("collection tenant configs cannot contain redacted placeholders")
				}
			}
		}
	}
	if c.Mode == "push" && c.Secret == "" && len(c.TenantBindings) == 0 {
		return fmt.Errorf("collection.secret is required for push")
	}
	if c.Secret == redactedSecret {
		return fmt.Errorf("collection.secret cannot be a redacted placeholder")
	}
	if c.Mode == "pull" && c.IntervalMinutes <= 0 {
		return fmt.Errorf("collection.interval_minutes must be positive for pull")
	}
	for _, value := range c.Configs {
		if value == redactedSecret {
			return fmt.Errorf("collection.configs cannot contain redacted placeholders")
		}
	}
	return nil
}

// WithPreservedCollectionSecrets 恢复普通管理查询返回的脱敏字段；省略整个 collection 表示移除接入配置。
func (s EventSource) WithPreservedCollectionSecrets(previous EventSource) EventSource {
	s = s.clone()
	if s.Collection == nil || previous.Collection == nil {
		return s
	}
	if s.Collection.Secret == redactedSecret {
		s.Collection.Secret = previous.Collection.Secret
	}
	for key, value := range s.Collection.Configs {
		if text, ok := value.(string); ok && text == redactedSecret {
			if old, exists := previous.Collection.Configs[key]; exists {
				s.Collection.Configs[key] = cloneProcessorConfigValue(old)
			}
		}
	}
	previousBindings := make(map[string]CollectionTenantBinding, len(previous.Collection.TenantBindings))
	for _, binding := range previous.Collection.TenantBindings {
		previousBindings[binding.TenantID] = binding
	}
	for i := range s.Collection.TenantBindings {
		binding := &s.Collection.TenantBindings[i]
		old, exists := previousBindings[binding.TenantID]
		if !exists {
			continue
		}
		if binding.Secret == redactedSecret {
			binding.Secret = old.Secret
		}
		for key, value := range binding.Configs {
			if text, ok := value.(string); ok && text == redactedSecret {
				if value, exists := old.Configs[key]; exists {
					binding.Configs[key] = cloneProcessorConfigValue(value)
				}
			}
		}
	}
	return s
}
