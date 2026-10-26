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
	"maps"
	"reflect"
	"regexp"

	"go.yaml.in/yaml/v3"
	"linkd/internal/enrich/kingeye"
	"linkd/internal/kafkaclient"
)

const (
	// HookTypeKAC 注册 KAC Alarm 兼容 Kafka 输出。
	HookTypeKAC = "kac"
	// HookTypeKafka 注册完整 Alert V1 Kafka 输出。
	HookTypeKafka = "kafka"
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
// 历史 Redis 索引参数只用于读取存量配置，ValidateHooks 禁止再次发布或执行。
type HookParameters struct {
	FieldMappings   map[string]string          `yaml:"field_mappings,omitempty" json:"field_mappings,omitempty"`
	Brokers         []string                   `yaml:"brokers,omitempty" json:"brokers,omitempty"`
	Topic           string                     `yaml:"topic,omitempty" json:"topic,omitempty"`
	ClientID        string                     `yaml:"client_id,omitempty" json:"client_id,omitempty"`
	MaxMessageBytes int                        `yaml:"max_message_bytes,omitempty" json:"max_message_bytes,omitempty"`
	Security        kafkaclient.SecurityConfig `yaml:"security,omitempty" json:"security,omitzero"`
	// 以下参数仅用于读取、脱敏已有 Record/历史 Release。新发布均拒绝，运行时没有旧索引 Hook。
	Redis               *RedisConfig `yaml:"redis,omitempty" json:"redis,omitempty"`
	KeyPrefix           string       `yaml:"key_prefix,omitempty" json:"key_prefix,omitempty"`
	TimeoutMilliseconds *int64       `yaml:"timeout_milliseconds,omitempty" json:"timeout_milliseconds,omitempty"`
	PoolSize            int          `yaml:"pool_size,omitempty" json:"pool_size,omitempty"`
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

	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*c = HookParameters(decoded)
	return nil
}

// UnmarshalYAML 与 JSON 使用相同的严格字段边界。
func (c *HookParameters) UnmarshalYAML(node *yaml.Node) error {
	type plain HookParameters
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("hook config must be an object")
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
	h.Config.FieldMappings = maps.Clone(h.Config.FieldMappings)
	h.Config.Brokers = append([]string(nil), h.Config.Brokers...)
	h.Config.Security = h.Config.Security.Clone()
	if h.Config.Redis != nil {
		value := h.Config.Redis.clone()
		h.Config.Redis = &value
	}
	if h.Config.TimeoutMilliseconds != nil {
		value := *h.Config.TimeoutMilliseconds
		h.Config.TimeoutMilliseconds = &value
	}

	return h
}

// WithDefaults 深拷贝参数，只为选中的插件补齐默认值。
func (h HookConfig) WithDefaults() HookConfig {
	h = h.clone()
	switch h.Type {
	case HookTypeKafka, HookTypeKAC:
		if h.Config.MaxMessageBytes == 0 {
			h.Config.MaxMessageBytes = 1 << 20
		}
		h.Config.Security = h.Config.Security.WithDefaults()

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

// WithPreservedHookSecrets 按实例名及插件类型恢复管理接口返回的脱敏凭据。
// 列表重排不改变凭据归属；新增、重命名或换类型的实例必须提交自己的凭据。
// 空 Redis 密码表示显式清除，只有占位符会恢复；省略整个 Kafka security 保留原安全配置。
func (s EventSource) WithPreservedHookSecrets(previous EventSource) EventSource {
	s = s.clone()
	for i := range s.Hooks {
		hook := &s.Hooks[i]
		for _, old := range previous.Hooks {
			if hook.Name != old.Name || hook.Type != old.Type {
				continue
			}
			currentSecurity, oldSecurity := &hook.Config.Security, old.Config.Security
			if reflect.ValueOf(*currentSecurity).IsZero() {
				*currentSecurity = oldSecurity.Clone()
			} else {
				if currentSecurity.SASL != nil && oldSecurity.SASL != nil && currentSecurity.SASL.Password == redactedSecret {
					currentSecurity.SASL.Password = oldSecurity.SASL.Password
				}
				if currentSecurity.TLS != nil && oldSecurity.TLS != nil && currentSecurity.TLS.ClientKeyPEM == redactedSecret {
					currentSecurity.TLS.ClientKeyPEM = oldSecurity.TLS.ClientKeyPEM
				}
			}

			break
		}
	}
	return s
}

// KafkaParameters 返回 Kafka 传输参数；Kafka V1 与 KAC Hook 共用配置形状。
func (h HookConfig) KafkaParameters() (brokers []string, topic, clientID string, maxMessageBytes int, security kafkaclient.SecurityConfig) {
	c := h.WithDefaults().Config
	return c.Brokers, c.Topic, c.ClientID, c.MaxMessageBytes, c.Security
}

// KafkaConfig 构造 Kafka V1 插件运行时参数；调用方须先验证插件类型。
func (h HookConfig) KafkaConfig() kafkaclient.ProducerConfig {
	c := h.WithDefaults().Config
	return kafkaclient.ProducerConfig{Brokers: c.Brokers, Topic: c.Topic, ClientID: c.ClientID, MaxMessageBytes: c.MaxMessageBytes, Security: c.Security}
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
	// API 在发布前恢复既有实例；无法恢复的占位符不能成为真实凭据，
	// YAML/provider 入口同样不能把脱敏配置误发布为可运行配置。
	if (c.Security.SASL != nil && c.Security.SASL.Password == redactedSecret) ||
		(c.Security.TLS != nil && c.Security.TLS.ClientKeyPEM == redactedSecret) {
		return fmt.Errorf("redacted hook credentials must be replaced with actual credentials")
	}
	if len(c.FieldMappings) > 0 {
		if h.Type != HookTypeKAC {
			return fmt.Errorf("field_mappings only applies to KAC")
		}
		if err := kingeye.ValidateFieldMappings(c.FieldMappings); err != nil {
			return err
		}
	}
	switch h.Type {
	case HookTypeKafka, HookTypeKAC:
		if c.Redis != nil || c.KeyPrefix != "" || c.TimeoutMilliseconds != nil || c.PoolSize != 0 {
			return fmt.Errorf("removed Redis hook parameters cannot be published")
		}
		return h.KafkaConfig().Validate()

	default:
		return fmt.Errorf("unknown hook type")
	}
}
