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
	"strings"
	"time"

	"linkd/internal/consume"
	"linkd/internal/consume/redisstream"
	"linkd/internal/kafkaclient"
	"linkd/internal/lifecycle/kafkahook"
	"linkd/internal/lifecycle/mailbox"
	"linkd/internal/lifecycle/scheduler"
)

const (
	defaultLifecycleConcurrency             = 8
	defaultLifecycleProcessTimeoutSeconds   = 30
	defaultLifecycleRetryMaxAttempts        = 3
	defaultLifecycleRetryMaxElapsedSeconds  = 120
	defaultSignalStream                     = "linkd:lifecycle:signals"
	defaultSignalGroup                      = "linkd-lifecycle"
	defaultSignalConsumerPrefix             = "linkd-lifecycle"
	defaultSignalReadBlockMilliseconds      = 1000
	defaultSignalClaimMinIdleSeconds        = 300
	defaultSignalMaxBatchMessages           = 128
	defaultSignalMaxMessageBytes            = 64 << 10
	defaultMailboxKeyPrefix                 = "linkd:lifecycle:mailbox"
	defaultMailboxMaxPending                = 128
	defaultMailboxMaxDrainEvents            = 512
	defaultMailboxBackpressureCacheTTL      = 3
	defaultMailboxBackpressureQueryTimeout  = 1
	defaultMailboxBackpressureHighWatermark = 100000
	defaultMailboxBackpressureLowWatermark  = 80000
	defaultLockKeyPrefix                    = "linkd:lifecycle:lock"
	defaultLockTTLSeconds                   = 60
	defaultLockRenewIntervalSeconds         = 20
	defaultLockRetryDelayMilliseconds       = 500
	defaultLockReleaseTimeoutSeconds        = 3
	defaultKafkaMaxMessageBytes             = 1 << 20
	maxLifecycleBatchBytes                  = 64 << 20
	maxLifecycleInflightBytes               = 256 << 20
)

// LifecycleConfig 描述 lifecycle 独立进程的消费、并发、锁和输出配置。
type LifecycleConfig struct {
	Concurrency            int                    `yaml:"concurrency"`
	ProcessTimeoutSeconds  int                    `yaml:"process_timeout_seconds"`
	RetryMaxAttempts       int                    `yaml:"retry_max_attempts"`
	RetryMaxElapsedSeconds int                    `yaml:"retry_max_elapsed_seconds"`
	Signal                 LifecycleSignalConfig  `yaml:"signal"`
	Mailbox                LifecycleMailboxConfig `yaml:"mailbox"`
	Lock                   LifecycleLockConfig    `yaml:"lock"`
	Output                 LifecycleOutputConfig  `yaml:"output"`
}

// LifecycleSignalConfig 描述 Redis Stream 和单批资源边界。
type LifecycleSignalConfig struct {
	Stream                string `yaml:"stream"`
	Group                 string `yaml:"group"`
	ConsumerPrefix        string `yaml:"consumer_prefix"`
	CreateGroup           *bool  `yaml:"create_group"`
	ReadBlockMilliseconds int    `yaml:"read_block_milliseconds"`
	ClaimMinIdleSeconds   int    `yaml:"claim_min_idle_seconds"`
	MaxBatchMessages      int    `yaml:"max_batch_messages"`
	MaxMessageBytes       int    `yaml:"max_message_bytes"`
}

// LifecycleMailboxConfig 描述 Redis Mailbox 容量和单次排空预算。
type LifecycleMailboxConfig struct {
	KeyPrefix      string                             `yaml:"key_prefix"`
	MaxPending     int                                `yaml:"max_pending"`
	MaxDrainEvents int                                `yaml:"max_drain_events"`
	Backpressure   LifecycleMailboxBackpressureConfig `yaml:"backpressure"`
}

// LifecycleMailboxBackpressureConfig 描述 Cleaner 对 lifecycle Signal 积压的近似背压边界。
type LifecycleMailboxBackpressureConfig struct {
	CacheTTLSeconds     int   `yaml:"cache_ttl_seconds"`
	QueryTimeoutSeconds int   `yaml:"query_timeout_seconds"`
	HighWatermark       int64 `yaml:"high_watermark"`
	LowWatermark        int64 `yaml:"low_watermark"`
}

// LifecycleLockConfig 描述 fingerprint Redis lease。
type LifecycleLockConfig struct {
	KeyPrefix              string `yaml:"key_prefix"`
	TTLSeconds             int    `yaml:"ttl_seconds"`
	RenewIntervalSeconds   int    `yaml:"renew_interval_seconds"`
	RetryDelayMilliseconds int    `yaml:"retry_delay_milliseconds"`
	ReleaseTimeoutSeconds  int    `yaml:"release_timeout_seconds"`
}

// LifecycleOutputConfig 描述 lifecycle 最终 Hook；当前只支持 Kafka。
type LifecycleOutputConfig struct {
	Kafka *LifecycleKafkaConfig `yaml:"kafka,omitempty"`
}

// LifecycleKafkaConfig 描述 Kafka FinalHook 的 producer 和 topic。
type LifecycleKafkaConfig struct {
	Brokers         []string                   `yaml:"brokers"`
	Topic           string                     `yaml:"topic"`
	ClientID        string                     `yaml:"client_id,omitempty"`
	MaxMessageBytes int                        `yaml:"max_message_bytes"`
	Security        kafkaclient.SecurityConfig `yaml:"security"`
}

// WithDefaults 返回补齐 lifecycle 默认值且不共享嵌套数据的副本。
func (c LifecycleConfig) WithDefaults() LifecycleConfig {
	if c.Concurrency == 0 {
		c.Concurrency = defaultLifecycleConcurrency
	}
	if c.ProcessTimeoutSeconds == 0 {
		c.ProcessTimeoutSeconds = defaultLifecycleProcessTimeoutSeconds
	}
	if c.RetryMaxAttempts == 0 {
		c.RetryMaxAttempts = defaultLifecycleRetryMaxAttempts
	}
	if c.RetryMaxElapsedSeconds == 0 {
		c.RetryMaxElapsedSeconds = defaultLifecycleRetryMaxElapsedSeconds
	}
	if c.Signal.Stream == "" {
		c.Signal.Stream = defaultSignalStream
	}
	if c.Signal.Group == "" {
		c.Signal.Group = defaultSignalGroup
	}
	if c.Signal.ConsumerPrefix == "" {
		c.Signal.ConsumerPrefix = defaultSignalConsumerPrefix
	}
	if c.Signal.CreateGroup == nil {
		createGroup := true
		c.Signal.CreateGroup = &createGroup
	} else {
		createGroup := *c.Signal.CreateGroup
		c.Signal.CreateGroup = &createGroup
	}
	if c.Signal.ReadBlockMilliseconds == 0 {
		c.Signal.ReadBlockMilliseconds = defaultSignalReadBlockMilliseconds
	}
	if c.Signal.ClaimMinIdleSeconds == 0 {
		c.Signal.ClaimMinIdleSeconds = defaultSignalClaimMinIdleSeconds
	}
	if c.Signal.MaxBatchMessages == 0 {
		c.Signal.MaxBatchMessages = defaultSignalMaxBatchMessages
	}
	if c.Signal.MaxMessageBytes == 0 {
		c.Signal.MaxMessageBytes = defaultSignalMaxMessageBytes
	}
	if c.Mailbox.KeyPrefix == "" {
		c.Mailbox.KeyPrefix = defaultMailboxKeyPrefix
	}
	if c.Mailbox.MaxPending == 0 {
		c.Mailbox.MaxPending = defaultMailboxMaxPending
	}
	if c.Mailbox.MaxDrainEvents == 0 {
		c.Mailbox.MaxDrainEvents = defaultMailboxMaxDrainEvents
	}
	if c.Mailbox.Backpressure.CacheTTLSeconds == 0 {
		c.Mailbox.Backpressure.CacheTTLSeconds = defaultMailboxBackpressureCacheTTL
	}
	if c.Mailbox.Backpressure.QueryTimeoutSeconds == 0 {
		c.Mailbox.Backpressure.QueryTimeoutSeconds = defaultMailboxBackpressureQueryTimeout
	}
	if c.Mailbox.Backpressure.HighWatermark == 0 {
		c.Mailbox.Backpressure.HighWatermark = defaultMailboxBackpressureHighWatermark
	}
	if c.Mailbox.Backpressure.LowWatermark == 0 {
		c.Mailbox.Backpressure.LowWatermark = defaultMailboxBackpressureLowWatermark
	}
	if c.Lock.KeyPrefix == "" {
		c.Lock.KeyPrefix = defaultLockKeyPrefix
	}
	if c.Lock.TTLSeconds == 0 {
		c.Lock.TTLSeconds = defaultLockTTLSeconds
	}
	if c.Lock.RenewIntervalSeconds == 0 {
		c.Lock.RenewIntervalSeconds = defaultLockRenewIntervalSeconds
	}
	if c.Lock.RetryDelayMilliseconds == 0 {
		c.Lock.RetryDelayMilliseconds = defaultLockRetryDelayMilliseconds
	}
	if c.Lock.ReleaseTimeoutSeconds == 0 {
		c.Lock.ReleaseTimeoutSeconds = defaultLockReleaseTimeoutSeconds
	}
	if c.Output.Kafka != nil {
		kafka := *c.Output.Kafka
		kafka.Brokers = append([]string(nil), c.Output.Kafka.Brokers...)
		if kafka.MaxMessageBytes == 0 {
			kafka.MaxMessageBytes = defaultKafkaMaxMessageBytes
		}
		kafka.Security = kafka.Security.WithDefaults()
		c.Output.Kafka = &kafka
	}
	return c
}

// Validate 校验 lifecycle 资源上限和跨组件时间预算。
func (c LifecycleConfig) Validate() error {
	c = c.WithDefaults()
	if c.Concurrency < 1 || c.Concurrency > 1024 {
		return fmt.Errorf("lifecycle.concurrency must be between 1 and 1024")
	}
	if c.ProcessTimeoutSeconds < 1 || c.ProcessTimeoutSeconds > 3600 {
		return fmt.Errorf("lifecycle.process_timeout_seconds must be between 1 and 3600")
	}
	if c.RetryMaxAttempts < 1 || c.RetryMaxAttempts > 20 {
		return fmt.Errorf("lifecycle.retry_max_attempts must be between 1 and 20")
	}
	if c.RetryMaxElapsedSeconds < 1 || c.RetryMaxElapsedSeconds > 3600 {
		return fmt.Errorf("lifecycle.retry_max_elapsed_seconds must be between 1 and 3600")
	}
	for name, value := range map[string]string{
		"stream":          c.Signal.Stream,
		"group":           c.Signal.Group,
		"consumer_prefix": c.Signal.ConsumerPrefix,
	} {
		if strings.TrimSpace(value) != value || value == "" || len(value) > 256 {
			return fmt.Errorf("lifecycle.signal.%s must be 1 to 256 bytes without surrounding whitespace", name)
		}
	}
	if c.Signal.ReadBlockMilliseconds < 1 || c.Signal.ReadBlockMilliseconds > 60000 {
		return fmt.Errorf("lifecycle.signal.read_block_milliseconds must be between 1 and 60000")
	}
	if c.Signal.ClaimMinIdleSeconds <= c.ProcessTimeoutSeconds+c.RetryMaxElapsedSeconds {
		return fmt.Errorf("lifecycle.signal.claim_min_idle_seconds must exceed process timeout plus retry elapsed")
	}
	if c.Signal.MaxBatchMessages < 1 || c.Signal.MaxBatchMessages > 4096 {
		return fmt.Errorf("lifecycle.signal.max_batch_messages must be between 1 and 4096")
	}
	if c.Signal.MaxMessageBytes < 1 || c.Signal.MaxMessageBytes > 1<<20 {
		return fmt.Errorf("lifecycle.signal.max_message_bytes must be between 1 and 1048576")
	}
	if err := c.MailboxConfig().Validate(); err != nil {
		return fmt.Errorf("lifecycle.mailbox: %w", err)
	}
	backpressure := c.Mailbox.Backpressure
	if backpressure.CacheTTLSeconds < 1 || backpressure.CacheTTLSeconds > 60 {
		return fmt.Errorf("lifecycle.mailbox.backpressure.cache_ttl_seconds must be between 1 and 60")
	}
	if backpressure.QueryTimeoutSeconds < 1 || backpressure.QueryTimeoutSeconds > backpressure.CacheTTLSeconds {
		return fmt.Errorf("lifecycle.mailbox.backpressure.query_timeout_seconds must be between 1 and cache_ttl_seconds")
	}
	if backpressure.LowWatermark <= 0 || backpressure.HighWatermark <= backpressure.LowWatermark {
		return fmt.Errorf("lifecycle.mailbox.backpressure watermarks must satisfy 0 < low_watermark < high_watermark")
	}
	batchBytes := int64(c.Signal.MaxBatchMessages) * int64(c.Signal.MaxMessageBytes)
	if batchBytes > maxLifecycleBatchBytes {
		return fmt.Errorf("lifecycle signal batch capacity must not exceed %d bytes", maxLifecycleBatchBytes)
	}
	inflightMessages := max(c.Concurrency*4, c.Signal.MaxBatchMessages)
	inflightBytes := int64(inflightMessages) * int64(c.Signal.MaxMessageBytes)
	if inflightBytes > maxLifecycleInflightBytes {
		return fmt.Errorf("lifecycle signal inflight capacity must not exceed %d bytes", maxLifecycleInflightBytes)
	}
	if strings.TrimSpace(c.Lock.KeyPrefix) != c.Lock.KeyPrefix || len(c.Lock.KeyPrefix) > 256 {
		return fmt.Errorf("lifecycle.lock.key_prefix must be at most 256 bytes without surrounding whitespace")
	}
	if err := c.SchedulerConfig().Validate(); err != nil {
		return fmt.Errorf("lifecycle.lock: %w", err)
	}
	if c.Output.Kafka == nil {
		return fmt.Errorf("lifecycle.output.kafka is required")
	}
	if err := c.KafkaHookConfig().Validate(); err != nil {
		return fmt.Errorf("lifecycle.output.kafka: %w", err)
	}
	return c.RuntimeConfig().Validate()
}

// Redacted 返回隐藏 Kafka SASL password 的深拷贝。
func (c LifecycleConfig) Redacted() LifecycleConfig {
	redacted := c.WithDefaults()
	if redacted.Output.Kafka != nil {
		redacted.Output.Kafka.Security = redacted.Output.Kafka.Security.Redacted()
	}
	return redacted
}

// RuntimeConfig 把用户可调并发和预算映射为有界消费运行时配置。
func (c LifecycleConfig) RuntimeConfig() consume.Config {
	c = c.WithDefaults()
	runtimeConfig := consume.DefaultConfig()
	runtimeConfig.WorkerCount = c.Concurrency
	runtimeConfig.MaxBatchMessages = c.Signal.MaxBatchMessages
	runtimeConfig.MaxBatchBytes = c.Signal.MaxBatchMessages * c.Signal.MaxMessageBytes
	runtimeConfig.MaxInflightMessages = max(c.Concurrency*4, c.Signal.MaxBatchMessages)
	runtimeConfig.MaxInflightBytes = runtimeConfig.MaxInflightMessages * c.Signal.MaxMessageBytes
	runtimeConfig.MaxInflightPerLane = runtimeConfig.MaxInflightMessages
	runtimeConfig.ProcessTimeout = time.Duration(c.ProcessTimeoutSeconds) * time.Second
	runtimeConfig.RetryMaxAttempts = c.RetryMaxAttempts
	runtimeConfig.RetryMaxElapsed = time.Duration(c.RetryMaxElapsedSeconds) * time.Second
	runtimeConfig.MaxRetryMessages = min(runtimeConfig.MaxInflightMessages, max(c.Concurrency*2, 1))
	return runtimeConfig
}

// RedisStreamConfig 构造 lifecycle signal Consumer Group 配置。
func (c LifecycleConfig) RedisStreamConfig(redisConfig RedisConfig, consumerName string) redisstream.Config {
	c = c.WithDefaults()
	return redisstream.Config{
		Address:        redisConfig.Address,
		Username:       redisConfig.Username,
		Password:       redisConfig.Password,
		DB:             redisConfig.Database,
		Stream:         c.Signal.Stream,
		Group:          c.Signal.Group,
		Consumer:       consumerName,
		CreateGroup:    *c.Signal.CreateGroup,
		ReadBlock:      time.Duration(c.Signal.ReadBlockMilliseconds) * time.Millisecond,
		ClaimMinIdle:   time.Duration(c.Signal.ClaimMinIdleSeconds) * time.Second,
		BodyField:      "payload",
		MessageIDField: "message_id",
		TenantIDField:  "bk_tenant_id",
		OrderKeyField:  "order_key",
	}
}

// SchedulerConfig 构造 fingerprint lease 配置。
func (c LifecycleConfig) SchedulerConfig() scheduler.Config {
	c = c.WithDefaults()
	return scheduler.Config{
		LockKeyPrefix:  c.Lock.KeyPrefix,
		LockTTL:        time.Duration(c.Lock.TTLSeconds) * time.Second,
		RenewInterval:  time.Duration(c.Lock.RenewIntervalSeconds) * time.Second,
		LockRetryDelay: time.Duration(c.Lock.RetryDelayMilliseconds) * time.Millisecond,
		ReleaseTimeout: time.Duration(c.Lock.ReleaseTimeoutSeconds) * time.Second,
		MaxDrainEvents: c.Mailbox.MaxDrainEvents,
	}
}

// MailboxConfig 构造 Redis Mailbox 配置。
func (c LifecycleConfig) MailboxConfig() mailbox.Config {
	c = c.WithDefaults()
	return mailbox.Config{
		KeyPrefix: c.Mailbox.KeyPrefix, SignalStream: c.Signal.Stream,
		MaxPendingPerMailbox: c.Mailbox.MaxPending,
	}
}

// KafkaHookConfig 构造同步 Kafka FinalHook 配置。
func (c LifecycleConfig) KafkaHookConfig() kafkahook.Config {
	c = c.WithDefaults()
	if c.Output.Kafka == nil {
		return kafkahook.Config{}
	}
	kafka := c.Output.Kafka
	return kafkahook.Config{
		Brokers:         append([]string(nil), kafka.Brokers...),
		Topic:           kafka.Topic,
		ClientID:        kafka.ClientID,
		MaxMessageBytes: kafka.MaxMessageBytes,
		Security:        kafka.Security.Clone(),
	}
}
