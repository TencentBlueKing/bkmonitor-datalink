// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/coordinator"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

const ModeShadow = "shadow"

type Duration time.Duration

func (d *Duration) UnmarshalText(text []byte) error {
	value, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("parse duration: %w", err)
	}
	*d = Duration(value)
	return nil
}

func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

type HTTPConfig struct {
	Listen string `yaml:"listen"`
}

type KafkaOutputConfig struct {
	Topic           string `yaml:"topic"`
	MaxMessageBytes int    `yaml:"max_message_bytes"`
}

type KafkaConfig struct {
	Brokers             []string          `yaml:"brokers"`
	InputTopic          string            `yaml:"input_topic"`
	TriggerEvent        KafkaOutputConfig `yaml:"trigger_event"`
	MessageReceipt      KafkaOutputConfig `yaml:"message_receipt"`
	AllowedOutputTopics []string          `yaml:"allowed_output_topics"`
	GroupID             string            `yaml:"group_id"`
	ClientID            string            `yaml:"client_id"`
	BrokerVersion       string            `yaml:"broker_version"`
	InitialOffset       string            `yaml:"initial_offset"`
}

func (c KafkaConfig) ConsumerCoordinates() enginekafka.Config {
	return enginekafka.Config{
		Brokers:       append([]string(nil), c.Brokers...),
		Topic:         c.InputTopic,
		GroupID:       c.GroupID,
		ClientID:      c.ClientID,
		BrokerVersion: c.BrokerVersion,
		InitialOffset: c.InitialOffset,
	}
}

func (c KafkaConfig) TriggerEventCoordinates() enginekafka.DecisionSinkConfig {
	return c.outputCoordinates(c.TriggerEvent)
}

func (c KafkaConfig) MessageReceiptCoordinates() enginekafka.DecisionSinkConfig {
	return c.outputCoordinates(c.MessageReceipt)
}

func (c KafkaConfig) outputCoordinates(output KafkaOutputConfig) enginekafka.DecisionSinkConfig {
	return enginekafka.DecisionSinkConfig{
		Brokers:             append([]string(nil), c.Brokers...),
		InputTopic:          c.InputTopic,
		OutputTopic:         output.Topic,
		AllowedOutputTopics: append([]string(nil), c.AllowedOutputTopics...),
		ClientID:            c.ClientID,
		BrokerVersion:       c.BrokerVersion,
		MaxMessageBytes:     output.MaxMessageBytes,
	}
}

type RedisConfig struct {
	Address       string   `yaml:"address"`
	Username      string   `yaml:"username"`
	Password      string   `yaml:"password"`
	DB            int      `yaml:"db"`
	DialTimeout   Duration `yaml:"dial_timeout"`
	ReadTimeout   Duration `yaml:"read_timeout"`
	WriteTimeout  Duration `yaml:"write_timeout"`
	PoolSize      int      `yaml:"pool_size"`
	StatePrefix   string   `yaml:"state_prefix"`
	MinTTL        Duration `yaml:"min_ttl"`
	MaxTTL        Duration `yaml:"max_ttl"`
	RestartMargin Duration `yaml:"restart_margin"`
}

type DependencyRetryConfig struct {
	MinDelay Duration `yaml:"min_delay"`
	MaxDelay Duration `yaml:"max_delay"`
}

type ReceiptQueueConfig struct {
	MaxQueuedMessages int `yaml:"max_queued_messages"`
	MaxQueuedBytes    int `yaml:"max_queued_bytes"`
}

type EvaluationRunnerConfig struct {
	MaxPreparationWorkers    int `yaml:"max_preparation_workers"`
	MaxStatefulWorkers       int `yaml:"max_stateful_workers"`
	MaxInflightMessages      int `yaml:"max_inflight_messages"`
	MaxInflightBytes         int `yaml:"max_inflight_bytes"`
	MaxRuntimeKeysPerMessage int `yaml:"max_runtime_keys_per_message"`
	MaxPendingKeyRefs        int `yaml:"max_pending_key_refs"`
}

type Config struct {
	Mode             string                 `yaml:"mode"`
	HTTP             HTTPConfig             `yaml:"http"`
	Kafka            KafkaConfig            `yaml:"kafka"`
	Redis            RedisConfig            `yaml:"redis"`
	Limits           LimitsConfig           `yaml:"limits"`
	DependencyRetry  DependencyRetryConfig  `yaml:"dependency_retry"`
	ReceiptQueue     ReceiptQueueConfig     `yaml:"receipt_queue"`
	EvaluationRunner EvaluationRunnerConfig `yaml:"evaluation_runner"`
	ShutdownTimeout  Duration               `yaml:"shutdown_timeout"`
}

func Default() Config {
	runner := coordinator.DefaultConcurrentRunnerLimits()
	return Config{
		Mode: ModeShadow,
		HTTP: HTTPConfig{
			Listen: "127.0.0.1:8080",
		},
		Kafka: KafkaConfig{
			TriggerEvent:   KafkaOutputConfig{MaxMessageBytes: defaultOutputMaxMessageBytes},
			MessageReceipt: KafkaOutputConfig{MaxMessageBytes: defaultOutputMaxMessageBytes},
		},
		Redis: RedisConfig{
			DialTimeout: Duration(3 * time.Second), ReadTimeout: Duration(3 * time.Second),
			WriteTimeout: Duration(3 * time.Second), PoolSize: 16,
			MinTTL: Duration(time.Minute), MaxTTL: Duration(30 * 24 * time.Hour), RestartMargin: Duration(10 * time.Minute),
		},
		Limits: defaultLimits(),
		DependencyRetry: DependencyRetryConfig{
			MinDelay: Duration(100 * time.Millisecond), MaxDelay: Duration(5 * time.Second),
		},
		ReceiptQueue: ReceiptQueueConfig{MaxQueuedMessages: 4096, MaxQueuedBytes: 16 << 20},
		EvaluationRunner: EvaluationRunnerConfig{
			MaxPreparationWorkers: runner.PreparationWorkers, MaxStatefulWorkers: runner.StatefulWorkers,
			MaxInflightMessages: runner.MaxInflightMessages, MaxInflightBytes: runner.MaxInflightBytes,
			MaxRuntimeKeysPerMessage: runner.MaxRuntimeKeysPerMessage, MaxPendingKeyRefs: runner.MaxPendingKeyRefs,
		},
		ShutdownTimeout: Duration(10 * time.Second),
	}
}

func (c Config) RedisBackendOptions() state.RedisBackendOptions {
	return state.RedisBackendOptions{
		Address: c.Redis.Address, Username: c.Redis.Username, Password: c.Redis.Password, DB: c.Redis.DB,
		DialTimeout: c.Redis.DialTimeout.Duration(), ReadTimeout: c.Redis.ReadTimeout.Duration(),
		WriteTimeout: c.Redis.WriteTimeout.Duration(), PoolSize: c.Redis.PoolSize,
	}
}

func (c Config) StateStoreOptions(codec *state.Codec, router state.StorageRouter, observer state.Observer) state.StoreOptions {
	return state.StoreOptions{
		Prefix: c.Redis.StatePrefix, Codec: codec, Router: router, Limits: c.StoreLimits(),
		MinTTL: c.Redis.MinTTL.Duration(), MaxTTL: c.Redis.MaxTTL.Duration(),
		RestartMargin: c.Redis.RestartMargin.Duration(), Observer: observer,
	}
}

func (c Config) DependencyRetryOptions() coordinator.DependencyRetryConfig {
	return coordinator.DependencyRetryConfig{
		MinDelay: c.DependencyRetry.MinDelay.Duration(), MaxDelay: c.DependencyRetry.MaxDelay.Duration(),
	}
}

func (c Config) ReceiptPublisherLimits() enginekafka.ReceiptPublisherLimits {
	return enginekafka.ReceiptPublisherLimits{
		MaxQueuedMessages: c.ReceiptQueue.MaxQueuedMessages, MaxQueuedBytes: c.ReceiptQueue.MaxQueuedBytes,
	}
}

func (c Config) EvaluationRunnerLimits() coordinator.ConcurrentRunnerLimits {
	return coordinator.ConcurrentRunnerLimits{
		PreparationWorkers:       c.EvaluationRunner.MaxPreparationWorkers,
		StatefulWorkers:          c.EvaluationRunner.MaxStatefulWorkers,
		MaxInflightMessages:      c.EvaluationRunner.MaxInflightMessages,
		MaxInflightBytes:         c.EvaluationRunner.MaxInflightBytes,
		MaxRuntimeKeysPerMessage: c.EvaluationRunner.MaxRuntimeKeysPerMessage,
		MaxPendingKeyRefs:        c.EvaluationRunner.MaxPendingKeyRefs,
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, cfg.Validate()
	}

	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("decode config: multiple YAML documents are not allowed")
		}
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.Mode != ModeShadow {
		return fmt.Errorf("mode %q is not allowed before production ownership is implemented", c.Mode)
	}

	host, port, err := net.SplitHostPort(c.HTTP.Listen)
	if err != nil {
		return fmt.Errorf("http listen %q: %w", c.HTTP.Listen, err)
	}
	if host == "" {
		return fmt.Errorf("http listen %q has empty host", c.HTTP.Listen)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber <= 0 || portNumber > 65535 {
		return fmt.Errorf("http listen %q has invalid port", c.HTTP.Listen)
	}

	if c.ShutdownTimeout.Duration() <= 0 {
		return errors.New("shutdown_timeout must be positive")
	}
	if err := c.Kafka.ConsumerCoordinates().Validate(); err != nil {
		return fmt.Errorf("consumer configuration: %w", err)
	}
	if c.Kafka.TriggerEvent.Topic == c.Kafka.MessageReceipt.Topic {
		return errors.New("kafka trigger_event and message_receipt topics must differ")
	}
	if err := c.Kafka.TriggerEventCoordinates().Validate(); err != nil {
		return fmt.Errorf("trigger event configuration: %w", err)
	}
	if err := c.Kafka.MessageReceiptCoordinates().Validate(); err != nil {
		return fmt.Errorf("message receipt configuration: %w", err)
	}
	if err := c.validateRedis(); err != nil {
		return err
	}
	if err := c.Limits.validate(); err != nil {
		return err
	}
	if c.Limits.Reader.MaxEnvelopeBytes > enginekafka.MaxConsumerRecordBytes() {
		return errors.New("limits.reader.max_envelope_bytes exceeds Kafka consumer record fetch budget")
	}
	if c.Limits.Trigger.MaxEvidenceBytesPerEvent > c.Kafka.TriggerEvent.MaxMessageBytes {
		return errors.New("trigger_event max_message_bytes cannot admit maximum trigger evidence")
	}
	if c.DependencyRetry.MinDelay.Duration() <= 0 || c.DependencyRetry.MaxDelay.Duration() < c.DependencyRetry.MinDelay.Duration() {
		return errors.New("dependency_retry delay range is invalid")
	}
	if c.ReceiptQueue.MaxQueuedMessages <= 0 || c.ReceiptQueue.MaxQueuedBytes <= 0 {
		return errors.New("receipt_queue budgets must be positive")
	}
	if c.ReceiptQueue.MaxQueuedBytes < c.Kafka.MessageReceipt.MaxMessageBytes {
		return errors.New("receipt_queue max_queued_bytes cannot admit one maximum message receipt")
	}
	if c.EvaluationRunner.MaxPreparationWorkers <= 0 || c.EvaluationRunner.MaxStatefulWorkers <= 0 ||
		c.EvaluationRunner.MaxInflightMessages <= 0 || c.EvaluationRunner.MaxInflightBytes <= 0 ||
		c.EvaluationRunner.MaxRuntimeKeysPerMessage <= 0 || c.EvaluationRunner.MaxPendingKeyRefs <= 0 ||
		c.EvaluationRunner.MaxRuntimeKeysPerMessage > c.EvaluationRunner.MaxPendingKeyRefs {
		return errors.New("evaluation_runner budgets must be positive and internally consistent")
	}
	if c.EvaluationRunner.MaxRuntimeKeysPerMessage/c.Limits.Reader.MaxPlansPerMessage <
		c.Limits.Reader.MaxRecordsPerMessage {
		return errors.New("evaluation_runner max_runtime_keys_per_message cannot admit one maximum reader message")
	}
	if c.EvaluationRunner.MaxInflightBytes < c.Limits.Reader.MaxEnvelopeBytes {
		return errors.New("evaluation_runner max_inflight_bytes cannot admit one maximum reader envelope")
	}
	return nil
}

func (c Config) validateRedis() error {
	if c.Redis.Address == "" || strings.TrimSpace(c.Redis.Address) != c.Redis.Address {
		return errors.New("redis address must be non-empty canonical text")
	}
	if c.Redis.DB < 0 || c.Redis.DialTimeout.Duration() <= 0 || c.Redis.ReadTimeout.Duration() <= 0 ||
		c.Redis.WriteTimeout.Duration() <= 0 || c.Redis.PoolSize <= 0 {
		return errors.New("redis db, timeouts and pool_size are invalid")
	}
	if strings.TrimSpace(c.Redis.StatePrefix) == "" || strings.TrimSpace(c.Redis.StatePrefix) != c.Redis.StatePrefix {
		return errors.New("redis state_prefix must be non-empty canonical text")
	}
	if c.Redis.MinTTL.Duration() <= 0 || c.Redis.MaxTTL.Duration() < c.Redis.MinTTL.Duration() || c.Redis.RestartMargin.Duration() < 0 {
		return errors.New("redis state TTL range is invalid")
	}
	return nil
}
