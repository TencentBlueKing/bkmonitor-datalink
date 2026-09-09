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
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
	"linkd/internal/kafkaclient"
	"linkd/internal/lifecycle/kafkahook"
)

const (
	// HookTypeKafka 注册完整 Alert V1 Kafka 输出。
	HookTypeKafka = "kafka"
	// HookTypeActiveAlertByStrategy 注册按策略维护活跃 fingerprint 的 Redis 输出。
	HookTypeActiveAlertByStrategy = "active-alert-by-strategy"
	// MaxHooksPerSource 限制每个任务持有的插件、连接和单次输出数量。
	MaxHooksPerSource = 16
)

var hookNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// HookConfig 是来源发布中的具名插件实例；Name 属于流水身份，不能使用列表位置代替。
type HookConfig struct {
	// Name 是来源内稳定且唯一的实例名。
	Name string `yaml:"name" json:"name"`
	// Type 选择内置注册实现。
	Type string `yaml:"type" json:"type"`
	// Config 只允许所选插件的参数。
	Config HookParameters `yaml:"config" json:"config"`
}

// HookParameters 是内置插件参数的封闭联合；ValidateHooks 拒绝其他插件的参数。
// Redis 和 TimeoutMilliseconds 使用指针区分缺省与显式空配置、零超时。
type HookParameters struct {
	Brokers             []string                   `yaml:"brokers,omitempty" json:"brokers,omitempty"`
	Topic               string                     `yaml:"topic,omitempty" json:"topic,omitempty"`
	ClientID            string                     `yaml:"client_id,omitempty" json:"client_id,omitempty"`
	MaxMessageBytes     int                        `yaml:"max_message_bytes,omitempty" json:"max_message_bytes,omitempty"`
	Security            kafkaclient.SecurityConfig `yaml:"security,omitempty" json:"security,omitzero"`
	Redis               *RedisConfig               `yaml:"redis,omitempty" json:"redis,omitempty"`
	KeyPrefix           string                     `yaml:"key_prefix,omitempty" json:"key_prefix,omitempty"`
	TimeoutMilliseconds *int64                     `yaml:"timeout_milliseconds,omitempty" json:"timeout_milliseconds,omitempty"`
}

// UnmarshalJSON 对插件参数执行严格解码，provider 和管理 API 使用相同字段边界。
func (c *HookParameters) UnmarshalJSON(data []byte) error {
	type plain HookParameters
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("hook config must be an object")
	}
	if raw, exists := fields["timeout_milliseconds"]; exists && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("timeout_milliseconds must be a positive integer")
	}
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*c = HookParameters(decoded)
	return nil
}

// UnmarshalYAML 与 JSON 保持严格字段及显式 null 超时的处理一致。
func (c *HookParameters) UnmarshalYAML(node *yaml.Node) error {
	type plain HookParameters
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("hook config must be an object")
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == "timeout_milliseconds" && node.Content[i+1].Tag == "!!null" {
			return fmt.Errorf("timeout_milliseconds must be a positive integer")
		}
	}
	data, err := yaml.Marshal(node)
	if err != nil {
		return err
	}
	var decoded plain
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*c = HookParameters(decoded)
	return nil
}

func (h HookConfig) clone() HookConfig {
	h.Config.Brokers = append([]string(nil), h.Config.Brokers...)
	h.Config.Security = h.Config.Security.Clone()
	if h.Config.Redis != nil {
		r := h.Config.Redis.clone()
		h.Config.Redis = &r
	}
	if h.Config.TimeoutMilliseconds != nil {
		ms := *h.Config.TimeoutMilliseconds
		h.Config.TimeoutMilliseconds = &ms
	}
	return h
}

// WithDefaults 深拷贝参数，只为选中的插件补齐默认值。
func (h HookConfig) WithDefaults() HookConfig {
	h = h.clone()
	switch h.Type {
	case HookTypeKafka:
		if h.Config.MaxMessageBytes == 0 {
			h.Config.MaxMessageBytes = 1 << 20
		}
		h.Config.Security = h.Config.Security.WithDefaults()
	case HookTypeActiveAlertByStrategy:
		if h.Config.TimeoutMilliseconds == nil {
			ms := int64(1000)
			h.Config.TimeoutMilliseconds = &ms
		}
		if h.Config.Redis != nil {
			r := h.Config.Redis.WithDefaults()
			h.Config.Redis = &r
		}
	}
	return h
}

// Redacted 返回可展示的副本，不修改发布配置中的凭据。
func (h HookConfig) Redacted() HookConfig {
	h = h.clone()
	h.Config.Security = h.Config.Security.Redacted()
	if h.Config.Redis != nil {
		h.Config.Redis = (StorageConfig{Redis: h.Config.Redis}).Redacted().Redis
	}
	return h
}

// KafkaConfig 构造 Kafka 插件运行时参数；调用方须先验证插件类型。
func (h HookConfig) KafkaConfig() kafkahook.Config {
	c := h.WithDefaults().Config
	return kafkahook.Config{Brokers: c.Brokers, Topic: c.Topic, ClientID: c.ClientID, MaxMessageBytes: c.MaxMessageBytes, Security: c.Security}
}

// ValidateHooks 在发布前校验注册类型、实例身份和各插件参数，不连接外部服务。
func ValidateHooks(hooks []HookConfig) error {
	if len(hooks) > MaxHooksPerSource {
		return fmt.Errorf("hooks must contain at most %d instances", MaxHooksPerSource)
	}
	seen := make(map[string]bool, len(hooks))
	for i, hook := range hooks {
		if !hookNamePattern.MatchString(hook.Name) || seen[hook.Name] {
			return fmt.Errorf("hooks[%d].name must be unique and contain 1 to 64 identifier characters", i)
		}
		seen[hook.Name] = true
		if err := hook.validate(); err != nil {
			return fmt.Errorf("hooks[%d].config: %w", i, err)
		}
	}
	return nil
}

func (h HookConfig) validate() error {
	c := h.WithDefaults().Config
	switch h.Type {
	case HookTypeKafka:
		if c.Redis != nil || c.KeyPrefix != "" || c.TimeoutMilliseconds != nil {
			return fmt.Errorf("kafka does not accept redis index parameters")
		}
		return h.KafkaConfig().Validate()
	case HookTypeActiveAlertByStrategy:
		if len(c.Brokers) != 0 || c.Topic != "" || c.ClientID != "" || c.MaxMessageBytes != 0 || !reflect.ValueOf(c.Security).IsZero() {
			return fmt.Errorf("active-alert-by-strategy does not accept kafka parameters")
		}
		if c.Redis == nil {
			return fmt.Errorf("redis is required")
		}
		if err := c.Redis.Validate(); err != nil {
			return fmt.Errorf("redis: %w", err)
		}
		if c.KeyPrefix == "" || strings.TrimSpace(c.KeyPrefix) != c.KeyPrefix || len(c.KeyPrefix) > 256 {
			return fmt.Errorf("key_prefix must be 1 to 256 bytes without surrounding whitespace")
		}
		if *c.TimeoutMilliseconds <= 0 || *c.TimeoutMilliseconds > math.MaxInt64/int64(time.Millisecond) {
			return fmt.Errorf("timeout_milliseconds must be positive and fit time.Duration")
		}
		return nil
	default:
		return fmt.Errorf("unknown hook type")
	}
}
