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
	"sort"
	"strings"
	"unicode"

	"linkd/internal/kafkaclient"
)

const (
	StorageTypeKafka      = "kafka"
	FingerprintModeField  = "field"
	FingerprintModeFields = "fields"
	CleanerTypeStandard   = "standard"
)

// EventSource 是一个全局唯一、供进程调度和事件标准化使用的事件源定义。
type EventSource struct {
	EventSourceID     string                   `yaml:"event_source_id"`
	RelatedTenantID   string                   `yaml:"related_tenant_id,omitempty"`
	Enabled           bool                     `yaml:"enabled"`
	Cleaner           CleanerConfig            `yaml:"cleaner"`
	FingerprintMode   string                   `yaml:"fingerprint_mode,omitempty"`
	FingerprintField  string                   `yaml:"fingerprint_field,omitempty"`
	FingerprintFields []string                 `yaml:"fingerprint_fields,omitempty"`
	SeverityMapping   map[string]string        `yaml:"severity_mapping,omitempty"`
	DefaultSeverity   string                   `yaml:"default_severity,omitempty"`
	Storage           EventSourceStorageConfig `yaml:"storage"`
}

// CleanerConfig 选择一个进程内注册的来源 Cleaner。
type CleanerConfig struct {
	Type    string                `yaml:"type"`
	Runtime *CleanerRuntimeConfig `yaml:"runtime,omitempty"`
}

// RuntimeConfig 将该事件源的非零局部预算覆盖到进程级 Cleaner 默认预算上。
func (c CleanerConfig) RuntimeConfig(global CleanerRuntimeConfig) CleanerRuntimeConfig {
	if c.Runtime == nil {
		return global.WithDefaults()
	}
	return MergeCleanerRuntime(global, *c.Runtime)
}

// EventSourceStorageConfig 定义 EventSource 当前使用的输入消息队列配置。
type EventSourceStorageConfig struct {
	Type  string             `yaml:"type"`
	Kafka KafkaStorageConfig `yaml:"kafka"`
}

// KafkaStorageConfig 定义 EventSource 的 Kafka subscription 与安全参数。
type KafkaStorageConfig struct {
	Brokers       []string                   `yaml:"brokers"`
	Topic         string                     `yaml:"topic"`
	ConsumerGroup string                     `yaml:"consumer_group"`
	Security      kafkaclient.SecurityConfig `yaml:"security"`
}

// WithDefaults 补齐 fingerprint、Cleaner 和安全协议默认值并深拷贝配置。
func (s EventSource) WithDefaults() EventSource {
	s = s.clone()
	if s.Cleaner.Type == "" {
		s.Cleaner.Type = CleanerTypeStandard
	}
	if s.FingerprintMode == "" {
		s.FingerprintMode = FingerprintModeField
	}
	if s.FingerprintMode == FingerprintModeField && s.FingerprintField == "" && len(s.FingerprintFields) == 0 {
		s.FingerprintField = "source_alert_id"
	}
	s.Storage.Kafka.Security = s.Storage.Kafka.Security.WithDefaults()
	return s
}

// Redacted 返回隐藏 Kafka 凭据且不共享嵌套可变数据的事件源副本。
func (s EventSource) Redacted() EventSource {
	redacted := s.clone()
	redacted.Storage.Kafka.Security = redacted.Storage.Kafka.Security.Redacted()
	return redacted
}

func (s EventSource) clone() EventSource {
	cloned := s
	if s.Cleaner.Runtime != nil {
		runtimeConfig := *s.Cleaner.Runtime
		cloned.Cleaner.Runtime = &runtimeConfig
	}
	cloned.FingerprintFields = append([]string(nil), s.FingerprintFields...)
	cloned.Storage.Kafka.Brokers = append([]string(nil), s.Storage.Kafka.Brokers...)
	cloned.Storage.Kafka.Security = s.Storage.Kafka.Security.Clone()
	if s.SeverityMapping != nil {
		cloned.SeverityMapping = make(map[string]string, len(s.SeverityMapping))
		for source, target := range s.SeverityMapping {
			cloned.SeverityMapping[source] = target
		}
	}
	return cloned
}

// ValidateEventSources 校验完整事件源清单及跨来源唯一性。
func ValidateEventSources(sources []EventSource, severity SeverityConfig) error {
	if err := severity.Validate(); err != nil {
		return err
	}
	ids := make(map[string]int, len(sources))
	subscriptions := make(map[subscriptionKey]int, len(sources))
	for index, source := range sources {
		source = source.WithDefaults()
		if err := source.validate(severity); err != nil {
			return fmt.Errorf("event_sources[%d].%w", index, err)
		}
		if previous, exists := ids[source.EventSourceID]; exists {
			return fmt.Errorf("event_sources[%d].event_source_id duplicates event_sources[%d]: %q", index, previous, source.EventSourceID)
		}
		ids[source.EventSourceID] = index
		key := source.subscriptionKey()
		if previous, exists := subscriptions[key]; exists {
			return fmt.Errorf("event_sources[%d].storage.kafka duplicates event_sources[%d] subscription", index, previous)
		}
		subscriptions[key] = index
	}
	return nil
}

func (s EventSource) validate(severity SeverityConfig) error {
	if err := validateBoundedText("event_source_id", s.EventSourceID, 1, 32); err != nil {
		return err
	}
	for _, r := range s.EventSourceID {
		if r != '_' && r != '-' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return fmt.Errorf("event_source_id has invalid format: %q", s.EventSourceID)
		}
	}
	if len(s.RelatedTenantID) > 64 {
		return fmt.Errorf("related_tenant_id must not exceed 64 bytes")
	}
	if s.RelatedTenantID != "" {
		for _, r := range s.RelatedTenantID {
			if r != '_' && r != '-' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
				return fmt.Errorf("related_tenant_id has invalid format: %q", s.RelatedTenantID)
			}
		}
	}
	if s.Cleaner.Type != CleanerTypeStandard {
		return fmt.Errorf("cleaner.type is not registered: %q", s.Cleaner.Type)
	}
	if err := s.validateFingerprint(); err != nil {
		return err
	}
	for sourceValue, target := range s.SeverityMapping {
		if strings.TrimSpace(sourceValue) == "" {
			return fmt.Errorf("severity_mapping source value must not be empty")
		}
		if !severity.Has(target) {
			return fmt.Errorf("severity_mapping[%q] references unknown severity %q", sourceValue, target)
		}
	}
	if s.DefaultSeverity != "" && !severity.Has(s.DefaultSeverity) {
		return fmt.Errorf("default_severity references unknown severity %q", s.DefaultSeverity)
	}
	if s.Storage.Type != StorageTypeKafka {
		return fmt.Errorf("storage.type must be %q: %q", StorageTypeKafka, s.Storage.Type)
	}
	if err := s.Storage.Kafka.validate(); err != nil {
		return fmt.Errorf("storage.kafka.%w", err)
	}
	return nil
}

func (s EventSource) validateFingerprint() error {
	switch s.FingerprintMode {
	case FingerprintModeField:
		if s.FingerprintField == "" || len(s.FingerprintFields) != 0 {
			return fmt.Errorf("fingerprint field mode requires exactly one field")
		}
		return validateFingerprintPath(s.FingerprintField)
	case FingerprintModeFields:
		if s.FingerprintField != "" || len(s.FingerprintFields) < 1 || len(s.FingerprintFields) > 32 {
			return fmt.Errorf("fingerprint fields mode requires 1 to 32 fields")
		}
		seen := make(map[string]struct{}, len(s.FingerprintFields))
		for _, field := range s.FingerprintFields {
			if err := validateFingerprintPath(field); err != nil {
				return err
			}
			if _, exists := seen[field]; exists {
				return fmt.Errorf("fingerprint field %q is duplicated", field)
			}
			seen[field] = struct{}{}
		}
		return nil
	default:
		return fmt.Errorf("fingerprint_mode must be field or fields: %q", s.FingerprintMode)
	}
}

func validateFingerprintPath(path string) error {
	if strings.HasPrefix(path, "dimensions.") && len(path) > len("dimensions.") &&
		!strings.Contains(strings.TrimPrefix(path, "dimensions."), ".") {
		return nil
	}
	for _, allowed := range []string{
		"source_alert_id", "condition_key", "subject_system", "subject_type", "subject_id",
	} {
		if path == allowed {
			return nil
		}
	}
	return fmt.Errorf("fingerprint field %q is not a stable Event path", path)
}

func (s EventSource) MapSeverity(raw string, severity SeverityConfig) (string, error) {
	if mapped, exists := s.SeverityMapping[raw]; exists {
		return mapped, nil
	}
	if severity.Has(raw) {
		return raw, nil
	}
	fallback := s.DefaultSeverity
	if fallback == "" {
		fallback = severity.WithDefaults().DefaultSeverity
	}
	if !severity.Has(fallback) {
		return "", fmt.Errorf("severity fallback is not defined: %q", fallback)
	}
	return fallback, nil
}

func (c KafkaStorageConfig) validate() error {
	if len(c.Brokers) == 0 {
		return fmt.Errorf("brokers must contain at least one broker")
	}
	if _, err := kafkaclient.NormalizeBrokers(c.Brokers); err != nil {
		return err
	}
	if err := kafkaclient.ValidateTopic(c.Topic); err != nil {
		return err
	}
	if err := validateText("consumer_group", c.ConsumerGroup); err != nil {
		return err
	}
	if _, err := c.Security.BuildTLSConfig(); err != nil {
		return fmt.Errorf("security.%w", err)
	}
	return nil
}

func validateText(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if strings.TrimSpace(value) != value || strings.ContainsFunc(value, unicode.IsControl) {
		return fmt.Errorf("%s must not contain surrounding whitespace or control characters", field)
	}
	return nil
}

func validateBoundedText(field, value string, minLength, maxLength int) error {
	if err := validateText(field, value); err != nil {
		return err
	}
	if len(value) < minLength || len(value) > maxLength {
		return fmt.Errorf("%s length must be between %d and %d bytes", field, minLength, maxLength)
	}
	return nil
}

type subscriptionKey struct{ brokers, topic, consumerGroup string }

func (s EventSource) subscriptionKey() subscriptionKey {
	brokers, _ := kafkaclient.NormalizeBrokers(s.Storage.Kafka.Brokers)
	sort.Strings(brokers)
	return subscriptionKey{strings.Join(brokers, "\x00"), s.Storage.Kafka.Topic, s.Storage.Kafka.ConsumerGroup}
}
