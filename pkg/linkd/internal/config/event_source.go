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
	// RuntimeClientID 由任务代次注入，不属于可编辑配置或发布摘要。
	RuntimeClientID   string                   `yaml:"-" json:"-"`
	Version           int64                    `yaml:"-" json:"-"`
	Scheduling        SourceScheduling         `yaml:"scheduling" json:"scheduling"`
	EventSourceID     string                   `yaml:"event_source_id" json:"event_source_id"`
	RelatedTenantID   string                   `yaml:"related_tenant_id,omitempty" json:"related_tenant_id,omitempty"`
	Enabled           bool                     `yaml:"enabled" json:"enabled"`
	Cleaner           CleanerConfig            `yaml:"cleaner" json:"cleaner"`
	FingerprintMode   string                   `yaml:"fingerprint_mode,omitempty" json:"fingerprint_mode,omitempty"`
	FingerprintField  string                   `yaml:"fingerprint_field,omitempty" json:"fingerprint_field,omitempty"`
	FingerprintFields []string                 `yaml:"fingerprint_fields,omitempty" json:"fingerprint_fields,omitempty"`
	SeverityMapping   map[string]string        `yaml:"severity_mapping,omitempty" json:"severity_mapping,omitempty"`
	DefaultSeverity   string                   `yaml:"default_severity,omitempty" json:"default_severity,omitempty"`
	Hooks             []HookConfig             `yaml:"hooks,omitempty" json:"hooks,omitempty"`
	Enrich            EnrichConfig             `yaml:"enrich,omitempty" json:"enrich,omitempty"`
	Storage           EventSourceStorageConfig `yaml:"storage" json:"storage"`
}

// EnrichConfig 定义该来源创建新 Alert 时按顺序执行的丰富处理链及其数据源。
type EnrichConfig struct {
	Processors  []EnrichProcessorConfig `yaml:"processors,omitempty" json:"processors,omitempty"`
	DataSources *EnrichDataSources      `yaml:"datasources,omitempty" json:"datasources,omitempty"`
}

// EnrichDataSources 定义该来源的丰富处理器可复用的物理数据源连接。
type EnrichDataSources struct {
	MySQL         *EnrichMySQLDataSource         `yaml:"mysql,omitempty" json:"mysql,omitempty"`
	Elasticsearch *EnrichElasticsearchDataSource `yaml:"elasticsearch,omitempty" json:"elasticsearch,omitempty"`
}

// EnrichMySQLDataSource 定义 Enrich 使用的 MySQL 只读连接。
type EnrichMySQLDataSource struct {
	Address  string `yaml:"address" json:"address"`
	Database string `yaml:"database" json:"database"`
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
}

// EnrichElasticsearchDataSource 定义 Enrich 使用的 Elasticsearch 只读连接。
type EnrichElasticsearchDataSource struct {
	Addresses   []string               `yaml:"addresses" json:"addresses"`
	IndexPrefix string                 `yaml:"index_prefix" json:"index_prefix"`
	APIKey      string                 `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	BasicAuth   *EnrichBasicAuthSource `yaml:"basic_auth,omitempty" json:"basic_auth,omitempty"`
}

// EnrichBasicAuthSource 定义 Enrich Elasticsearch 的 Basic Auth 凭据。
type EnrichBasicAuthSource struct {
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
}

func (c EnrichConfig) clone() EnrichConfig {
	cloned := c
	cloned.Processors = append([]EnrichProcessorConfig(nil), c.Processors...)
	if c.DataSources != nil {
		dataSources := c.DataSources.clone()
		cloned.DataSources = &dataSources
	}
	return cloned
}

func (c EnrichDataSources) clone() EnrichDataSources {
	cloned := c
	if c.MySQL != nil {
		value := *c.MySQL
		cloned.MySQL = &value
	}
	if c.Elasticsearch != nil {
		value := *c.Elasticsearch
		value.Addresses = append([]string(nil), c.Elasticsearch.Addresses...)
		if c.Elasticsearch.BasicAuth != nil {
			basicAuth := *c.Elasticsearch.BasicAuth
			value.BasicAuth = &basicAuth
		}
		cloned.Elasticsearch = &value
	}
	return cloned
}

// WithPreservedSecrets 用已有配置补齐管理接口中省略或脱敏的数据源凭据。
func (c EnrichConfig) WithPreservedSecrets(previous EnrichConfig) EnrichConfig {
	merged := c.clone()
	if merged.DataSources == nil && previous.DataSources != nil {
		dataSources := previous.DataSources.clone()
		merged.DataSources = &dataSources
	}
	if merged.DataSources == nil || previous.DataSources == nil {
		return merged
	}
	if merged.DataSources.MySQL != nil && previous.DataSources.MySQL != nil &&
		(merged.DataSources.MySQL.Password == "" || merged.DataSources.MySQL.Password == redactedSecret) {
		merged.DataSources.MySQL.Password = previous.DataSources.MySQL.Password
	}
	currentElasticsearch, oldElasticsearch := merged.DataSources.Elasticsearch, previous.DataSources.Elasticsearch
	if currentElasticsearch != nil && oldElasticsearch != nil {
		if currentElasticsearch.APIKey == "" || currentElasticsearch.APIKey == redactedSecret {
			currentElasticsearch.APIKey = oldElasticsearch.APIKey
		}
		if currentElasticsearch.BasicAuth != nil && oldElasticsearch.BasicAuth != nil &&
			(currentElasticsearch.BasicAuth.Password == "" || currentElasticsearch.BasicAuth.Password == redactedSecret) {
			currentElasticsearch.BasicAuth.Password = oldElasticsearch.BasicAuth.Password
		}
	}
	return merged
}

func (c EnrichDataSources) redacted() EnrichDataSources {
	redacted := c.clone()
	if redacted.MySQL != nil && redacted.MySQL.Password != "" {
		redacted.MySQL.Password = redactedSecret
	}
	if redacted.Elasticsearch != nil {
		if redacted.Elasticsearch.APIKey != "" {
			redacted.Elasticsearch.APIKey = redactedSecret
		}
		if redacted.Elasticsearch.BasicAuth != nil && redacted.Elasticsearch.BasicAuth.Password != "" {
			redacted.Elasticsearch.BasicAuth.Password = redactedSecret
		}
	}
	return redacted
}

func (c EnrichMySQLDataSource) validate() error {
	return MySQLConfig(c).Validate()
}

func (c EnrichElasticsearchDataSource) validate() error {
	var basicAuth *BasicAuthConfig
	if c.BasicAuth != nil {
		basicAuth = &BasicAuthConfig{Username: c.BasicAuth.Username, Password: c.BasicAuth.Password}
	}
	return (ElasticsearchConfig{
		Addresses: c.Addresses, IndexPrefix: c.IndexPrefix,
		APIKey: c.APIKey, BasicAuth: basicAuth,
	}).Validate()
}

func (c EnrichConfig) validate() error {
	seenProcessors := make(map[string]int, len(c.Processors))
	for index, processor := range c.Processors {
		if strings.TrimSpace(processor.Type) == "" {
			return fmt.Errorf("enrich.processors[%d].type is required", index)
		}
		if previous, exists := seenProcessors[processor.Type]; exists {
			return fmt.Errorf("enrich.processors[%d].type duplicates enrich.processors[%d]: %q", index, previous, processor.Type)
		}
		seenProcessors[processor.Type] = index
	}
	_, err := c.SelectDataSources()
	if err != nil {
		return err
	}
	if c.DataSources != nil && c.DataSources.MySQL != nil {
		if err := c.DataSources.MySQL.validate(); err != nil {
			return fmt.Errorf("enrich.datasources.mysql: %w", err)
		}
	}
	if c.DataSources != nil && c.DataSources.Elasticsearch != nil {
		if err := c.DataSources.Elasticsearch.validate(); err != nil {
			return fmt.Errorf("enrich.datasources.elasticsearch: %w", err)
		}
	}
	return nil
}

// SelectDataSources 按 Processor Chain 返回实际需要绑定的物理连接。
// 全部 Processor 共享一个 MySQL 连接池；Strategy 和 Resource 额外共享 Elasticsearch Transport。
func (c EnrichConfig) SelectDataSources() (EnrichDataSources, error) {
	configured := EnrichDataSources{}
	if c.DataSources != nil {
		configured = *c.DataSources
	}
	selected := EnrichDataSources{}
	for _, processor := range c.Processors {
		switch processor.Type {
		case "strategy", "resource":
			if configured.MySQL == nil {
				return EnrichDataSources{}, fmt.Errorf("enrich.datasources.mysql is required by configured enrich processors")
			}
			if configured.Elasticsearch == nil {
				return EnrichDataSources{}, fmt.Errorf("enrich.datasources.elasticsearch is required by configured enrich processors")
			}
			selected.MySQL, selected.Elasticsearch = configured.MySQL, configured.Elasticsearch
		case "display", "metric", "source":
			if configured.MySQL == nil {
				return EnrichDataSources{}, fmt.Errorf("enrich.datasources.mysql is required by configured enrich processors")
			}
			selected.MySQL = configured.MySQL
		default:
			return EnrichDataSources{}, fmt.Errorf("enrich processor type is not registered: %q", processor.Type)
		}
	}
	return selected, nil
}

// EnrichProcessorConfig 通过稳定注册名选择丰富处理器。
type EnrichProcessorConfig struct {
	Type string `yaml:"type" json:"type"`
}

// CleanerConfig 选择一个进程内注册的来源 Cleaner。
type CleanerConfig struct {
	Type    string                `yaml:"type" json:"type"`
	Runtime *CleanerRuntimeConfig `yaml:"runtime,omitempty" json:"runtime,omitempty"`
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
	Type  string             `yaml:"type" json:"type"`
	Kafka KafkaStorageConfig `yaml:"kafka" json:"kafka"`
}

// KafkaStorageConfig 定义 EventSource 的 Kafka subscription 与安全参数。
type KafkaStorageConfig struct {
	Brokers       []string `yaml:"brokers" json:"brokers"`
	Topic         string   `yaml:"topic" json:"topic"`
	ConsumerGroup string   `yaml:"consumer_group" json:"consumer_group"`
	// FetchMaxWaitMilliseconds 限制 broker 空拉取等待；不控制 Cleaner 或 ES 合批。
	FetchMaxWaitMilliseconds int                        `yaml:"fetch_max_wait_milliseconds" json:"fetch_max_wait_milliseconds"`
	Security                 kafkaclient.SecurityConfig `yaml:"security" json:"security"`
}

// WithDefaults 补齐 fingerprint、Cleaner 和安全协议默认值并深拷贝配置。
func (s EventSource) WithDefaults() EventSource {
	s = s.clone()
	if s.Version == 0 {
		s.Version = 1
	}
	if s.Cleaner.Type == "" {
		s.Cleaner.Type = CleanerTypeStandard
	}
	if s.FingerprintMode == "" {
		s.FingerprintMode = FingerprintModeField
	}
	if s.FingerprintMode == FingerprintModeField && s.FingerprintField == "" && len(s.FingerprintFields) == 0 {
		s.FingerprintField = "source_alert_id"
	}
	for i := range s.Hooks {
		s.Hooks[i] = s.Hooks[i].WithDefaults()
	}
	s.Storage.Kafka.Security = s.Storage.Kafka.Security.WithDefaults()
	if s.Storage.Kafka.FetchMaxWaitMilliseconds == 0 {
		s.Storage.Kafka.FetchMaxWaitMilliseconds = 100
	}
	return s
}

// Redacted 返回隐藏 Kafka 凭据且不共享嵌套可变数据的事件源副本。
func (s EventSource) Redacted() EventSource {
	redacted := s.clone()
	redacted.Storage.Kafka.Security = redacted.Storage.Kafka.Security.Redacted()
	for i := range redacted.Hooks {
		redacted.Hooks[i] = redacted.Hooks[i].Redacted()
	}
	if redacted.Enrich.DataSources != nil {
		dataSources := redacted.Enrich.DataSources.redacted()
		redacted.Enrich.DataSources = &dataSources
	}
	return redacted
}

func (s EventSource) clone() EventSource {
	cloned := s
	cloned.Hooks = make([]HookConfig, len(s.Hooks))
	for i := range s.Hooks {
		cloned.Hooks[i] = s.Hooks[i].clone()
	}
	cloned.Scheduling = s.Scheduling.Clone()
	if s.Cleaner.Runtime != nil {
		runtimeConfig := *s.Cleaner.Runtime
		cloned.Cleaner.Runtime = &runtimeConfig
	}
	cloned.FingerprintFields = append([]string(nil), s.FingerprintFields...)
	cloned.Enrich = s.Enrich.clone()
	cloned.Storage.Kafka.Brokers = append([]string(nil), s.Storage.Kafka.Brokers...)
	cloned.Storage.Kafka.Security = s.Storage.Kafka.Security.Clone()
	if len(s.SeverityMapping) == 0 {
		cloned.SeverityMapping = nil
	}
	if len(s.SeverityMapping) > 0 {
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
	if err := s.Scheduling.Validate(); err != nil {
		return err
	}
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
	if err := ValidateHooks(s.Hooks); err != nil {
		return err
	}
	if err := s.Enrich.validate(); err != nil {
		return err
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
		"source_alert_id", "subject_system", "subject_type", "subject_id",
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
	if c.FetchMaxWaitMilliseconds < 10 || c.FetchMaxWaitMilliseconds > 5000 {
		return fmt.Errorf("fetch_max_wait_milliseconds must be between 10 and 5000")
	}
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
