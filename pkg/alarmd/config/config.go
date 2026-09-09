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
	"math"
	"net"
	"os"
	"runtime"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/coordinator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
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
	// Listen carries health, metrics and the observability API. It is the only
	// surface a host platform may route to.
	Listen string `yaml:"listen"`
	// DiagnosticsListen carries pprof on its own listener, defaulting to
	// loopback so routing the query surface cannot expose it. An empty value
	// serves no diagnostics at all; pprof is never folded back into Listen.
	DiagnosticsListen string `yaml:"diagnostics_listen"`
}

// DiagnosticsFact renders the diagnostics surface for startup logging. Every
// runtime reports it through this one helper so the three of them cannot drift
// into disagreeing about what an unset address means.
func (c HTTPConfig) DiagnosticsFact() string {
	if c.DiagnosticsListen == "" {
		return "disabled"
	}
	return c.DiagnosticsListen
}

type KafkaOutputConfig struct {
	Topic           string `yaml:"topic"`
	MaxMessageBytes int    `yaml:"max_message_bytes"`
}

type KafkaConfig struct {
	LegacyAdapter       LegacyAdapterConfig `yaml:"legacy_adapter"`
	Brokers             []string            `yaml:"brokers"`
	InputTopic          string              `yaml:"input_topic"`
	TriggerEvent        KafkaOutputConfig   `yaml:"trigger_event"`
	MessageReceipt      KafkaOutputConfig   `yaml:"message_receipt"`
	AllowedOutputTopics []string            `yaml:"allowed_output_topics"`
	GroupID             string              `yaml:"group_id"`
	ClientID            string              `yaml:"client_id"`
	BrokerVersion       string              `yaml:"broker_version"`
	InitialOffset       string              `yaml:"initial_offset"`
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
	RedisConnectionConfig `yaml:",inline"`
	StatePrefix           string   `yaml:"state_prefix"`
	MinTTL                Duration `yaml:"min_ttl"`
	MaxTTL                Duration `yaml:"max_ttl"`
	RestartMargin         Duration `yaml:"restart_margin"`
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
	Input            PhaseTwoInputConfig    `yaml:"input"`
	HTTP             HTTPConfig             `yaml:"http"`
	Kafka            KafkaConfig            `yaml:"kafka"`
	Redis            RedisConfig            `yaml:"redis"`
	Limits           LimitsConfig           `yaml:"limits"`
	DependencyRetry  DependencyRetryConfig  `yaml:"dependency_retry"`
	ReceiptQueue     ReceiptQueueConfig     `yaml:"receipt_queue"`
	EvaluationRunner EvaluationRunnerConfig `yaml:"evaluation_runner"`
	PhaseTwo         PhaseTwoRuntimeConfig  `yaml:"phase_two"`
	ShutdownTimeout  Duration               `yaml:"shutdown_timeout"`
}

func Default() Config {
	runner := coordinator.DefaultConcurrentRunnerLimits()
	return Config{
		Mode:  ModeShadow,
		Input: DefaultPhaseTwoInput(),
		HTTP: HTTPConfig{
			Listen: "127.0.0.1:8080",
			// 6060 rather than 8081: the comparator's query surface already
			// defaults to 8081, and two binaries started locally with defaults
			// would now fail to start instead of merely sharing a mux.
			DiagnosticsListen: "127.0.0.1:6060",
		},
		Kafka: KafkaConfig{
			TriggerEvent:        KafkaOutputConfig{Topic: "alarmd_event", MaxMessageBytes: defaultOutputMaxMessageBytes},
			LegacyAdapter:       LegacyAdapterConfig{Topic: "alarmd_0bkmonitor_backend_event"},
			AllowedOutputTopics: []string{"alarmd_event", "alarmd_0bkmonitor_backend_event"},
			MessageReceipt:      KafkaOutputConfig{MaxMessageBytes: defaultOutputMaxMessageBytes},
		},
		Redis: RedisConfig{
			RedisConnectionConfig: RedisConnectionConfig{Mode: RedisModeStandalone,
				DialTimeout: Duration(3 * time.Second), ReadTimeout: Duration(3 * time.Second),
				WriteTimeout: Duration(3 * time.Second), PoolSize: 0},
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
		PhaseTwo:        defaultPhaseTwoRuntime(),
		ShutdownTimeout: Duration(10 * time.Second),
	}
}

// AdmittedQueryConcurrency is the number of queries the scheduler may have in
// flight at once. It bounds the query stage only; it is not the bound on Redis
// concurrency, because the Slot source reads the control plane before a Slot
// becomes eligible to query and holds no permit while doing so.
func (c Config) AdmittedQueryConcurrency() int {
	s := c.PhaseTwo.Scheduler
	return s.ProcessQueryPermits + s.RecoveryQueryPermits
}

// redisPoolCPUBudget is the CPU budget the pool derives from. automaxprocs has
// already resolved GOMAXPROCS from the container's cgroup quota by the time the
// configuration is read, so this reports what the container was actually given
// rather than the host's core count.
func redisPoolCPUBudget() int {
	return runtime.GOMAXPROCS(0)
}

// WithResolvedRedisPoolSize returns a copy whose Redis pool sizes are concrete
// positive numbers, so the resolved value can be reported as a startup fact
// rather than staying implicit in the client.
func (c Config) WithResolvedRedisPoolSize() Config {
	cpuBudget := redisPoolCPUBudget()
	c.Redis.PoolSize = c.Redis.Connection().EffectivePoolSize(cpuBudget)
	if c.PhaseTwo.RuntimeRedis != nil {
		runtimeRedis := c.PhaseTwo.RuntimeRedis.clone()
		runtimeRedis.PoolSize = runtimeRedis.EffectivePoolSize(cpuBudget)
		c.PhaseTwo.RuntimeRedis = &runtimeRedis
	}
	return c
}

func (c Config) RedisBackendOptions() state.RedisBackendOptions {
	return state.RedisBackendOptions{
		Address: c.Redis.Address, Username: c.Redis.Username, Password: c.Redis.Password, DB: c.Redis.DB,
		DialTimeout: c.Redis.DialTimeout.Duration(), ReadTimeout: c.Redis.ReadTimeout.Duration(),
		WriteTimeout: c.Redis.WriteTimeout.Duration(),
		// Resolve here as well as in the runtime path: WithResolvedRedisPoolSize
		// carries the authoritative value into the startup facts, but options can
		// also be built by paths that never ran it, and a zero must never reach a
		// client.
		PoolSize: c.Redis.Connection().EffectivePoolSize(c.AdmittedQueryConcurrency()),
	}
}

func (c RedisConfig) Connection() RedisConnectionConfig {
	return c.RedisConnectionConfig.clone()
}

func (c Config) StrategySourceRedis() RedisConnectionConfig {
	return c.Redis.Connection()
}

func (c Config) ResolvedRuntimeRedis() RedisConnectionConfig {
	if c.PhaseTwo.RuntimeRedis != nil {
		return c.PhaseTwo.RuntimeRedis.clone()
	}
	return c.Redis.Connection()
}

func (c *Config) resolvePhaseTwoRuntimeRedis() {
	if c == nil || c.Input.Mode != InputModeGoAccess || c.PhaseTwo.RuntimeRedis != nil {
		return
	}
	resolved := c.Redis.Connection()
	c.PhaseTwo.RuntimeRedis = &resolved
}

// resolveCompatibilityServiceTimeouts lets a compatibility service node inherit
// the runtime Redis timeouts when it does not state its own. Timeouts say how
// long to wait, not where to write, so inheriting them keeps one operational
// setting instead of two; mode and address stay explicit because they decide
// the destination and a wrong guess there writes a snapshot nobody reads.
func (c *Config) resolveCompatibilityServiceTimeouts() {
	if c == nil || len(c.Kafka.LegacyAdapter.ServiceNodes) == 0 {
		return
	}
	runtimeRedis := c.Redis.Connection()
	resolved := make(map[string]RedisConnectionConfig, len(c.Kafka.LegacyAdapter.ServiceNodes))
	for id, connection := range c.Kafka.LegacyAdapter.ServiceNodes {
		if connection.DialTimeout == 0 {
			connection.DialTimeout = runtimeRedis.DialTimeout
		}
		if connection.ReadTimeout == 0 {
			connection.ReadTimeout = runtimeRedis.ReadTimeout
		}
		if connection.WriteTimeout == 0 {
			connection.WriteTimeout = runtimeRedis.WriteTimeout
		}
		resolved[id] = connection
	}
	c.Kafka.LegacyAdapter.ServiceNodes = resolved
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
		cfg.resolvePhaseTwoRuntimeRedis()
		cfg.resolveCompatibilityServiceTimeouts()
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

	cfg.resolvePhaseTwoRuntimeRedis()
	cfg.resolveCompatibilityServiceTimeouts()
	cfg.resolvePhaseTwoWorkerIDFromEnvironment()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	if cfg.Input.Mode == InputModePhaseOneKafkaCompatibility {
		cfg, err = cfg.PhaseOneCompatibilityRuntimeConfig()
		if err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if err := c.validateCommon(); err != nil {
		return err
	}
	if err := c.Input.Validate(); err != nil {
		return fmt.Errorf("input configuration: %w", err)
	}

	switch c.Input.Mode {
	case InputModeGoAccess:
		return c.validateGoAccessRuntime()
	case InputModePhaseOneKafkaCompatibility:
		runtimeConfig, err := c.PhaseOneCompatibilityRuntimeConfig()
		if err != nil {
			return err
		}
		return runtimeConfig.validatePhaseOneRuntime()
	default:
		return fmt.Errorf("input configuration: phase-two input mode %q is not supported", c.Input.Mode)
	}
}

func validateListenAddress(field, address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%s %q: %w", field, address, err)
	}
	if host == "" {
		return fmt.Errorf("%s %q has empty host", field, address)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber <= 0 || portNumber > 65535 {
		return fmt.Errorf("%s %q has invalid port", field, address)
	}
	return nil
}

// listenAddressesCollide reports whether two listen addresses would contend for
// the same socket. Comparing the strings is not enough: the deployed query
// surface binds a wildcard, so a diagnostics address on the same port with any
// host still fails to bind. Catching it here turns a CrashLoop into a config
// error. Host names beyond localhost are left to the bind, which fails loudly.
func listenAddressesCollide(left, right string) bool {
	leftHost, leftPort, err := net.SplitHostPort(left)
	if err != nil {
		return false
	}
	rightHost, rightPort, err := net.SplitHostPort(right)
	if err != nil {
		return false
	}
	if leftPort != rightPort {
		return false
	}
	return isWildcardHost(leftHost) || isWildcardHost(rightHost) ||
		normalizeListenHost(leftHost) == normalizeListenHost(rightHost)
}

func isWildcardHost(host string) bool {
	return host == "" || host == "0.0.0.0" || host == "::"
}

func normalizeListenHost(host string) string {
	if host == "localhost" {
		return "127.0.0.1"
	}
	return host
}

func (c Config) validateCommon() error {
	if c.Mode != ModeShadow {
		return fmt.Errorf("mode %q is not allowed before production ownership is implemented", c.Mode)
	}

	if err := validateListenAddress("http listen", c.HTTP.Listen); err != nil {
		return err
	}
	// An empty diagnostics address serves no pprof, which is a supported
	// choice; a set one must be a real address and must not collide with the
	// query surface, or the split it exists to create would not happen.
	if c.HTTP.DiagnosticsListen != "" {
		if err := validateListenAddress("http diagnostics_listen", c.HTTP.DiagnosticsListen); err != nil {
			return err
		}
		if listenAddressesCollide(c.HTTP.Listen, c.HTTP.DiagnosticsListen) {
			return fmt.Errorf(
				"http diagnostics_listen %q must differ from http listen %q",
				c.HTTP.DiagnosticsListen, c.HTTP.Listen,
			)
		}
	}

	if c.ShutdownTimeout.Duration() <= 0 {
		return errors.New("shutdown_timeout must be positive")
	}
	return nil
}

func (c Config) validateGoAccessRuntime() error {
	if c.Kafka.InputTopic != "" || c.Kafka.GroupID != "" || c.Kafka.InitialOffset != "" {
		return errors.New("phase-two Go Access must not configure phase-one Kafka input coordinates")
	}
	if c.Kafka.MessageReceipt.Topic != "" {
		return errors.New("phase-two Go Access must not configure the phase-one message receipt topic")
	}
	if err := validatePhaseTwoKafkaOutput(c.Kafka); err != nil {
		return fmt.Errorf("trigger event configuration: %w", err)
	}
	if err := c.Kafka.validateCompatibilityOutput(); err != nil {
		return fmt.Errorf("compatibility output configuration: %w", err)
	}
	if err := c.validateSharedRuntime(); err != nil {
		return err
	}
	if err := c.PhaseTwo.validate(); err != nil {
		return err
	}
	if err := c.ResolvedRuntimeRedis().validate("phase_two.runtime_redis"); err != nil {
		return err
	}
	if err := validateRuntimePrefixIsolation(c.Redis.StatePrefix, c.PhaseTwo.Control.StrategyCachePrefix); err != nil {
		return err
	}
	budget := c.PhaseTwo.Coordinator
	// One Slot's State or Gap mutations are applied in successive Store calls
	// of at most max_keys_per_batch items, up to StateApplyMaxChunks calls.
	// A process budget above that product could admit a Slot no apply can
	// ever carry, so it is rejected here rather than at runtime.
	chunkedApplyBudget := uint64(c.Limits.Store.MaxKeysPerBatch) * execution.StateApplyMaxChunks
	if budget.MaxStateMutations > chunkedApplyBudget || budget.MaxGapMutations > chunkedApplyBudget {
		return errors.New("phase_two mutation budgets exceed the chunked state store apply budget")
	}
	if budget.MaxRetainedBytes > math.MaxInt64 ||
		budget.MaxSeries > math.MaxUint64/c.Limits.Detect.MaxRecordsPerSeries {
		return errors.New("phase_two provider budgets overflow production limits")
	}
	if c.Limits.Trigger.MaxEvidenceBytesPerEvent > c.Kafka.TriggerEvent.MaxMessageBytes {
		return errors.New("trigger_event max_message_bytes cannot admit maximum trigger evidence")
	}
	return nil
}

func (c Config) validatePhaseOneRuntime() error {
	if c.PhaseTwo.RuntimeRedis != nil {
		return errors.New("phase-one Kafka compatibility must not configure phase_two runtime_redis")
	}
	if c.Redis.Mode != RedisModeStandalone {
		return errors.New("phase-one Kafka compatibility requires standalone Redis")
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
	if err := c.validateSharedRuntime(); err != nil {
		return err
	}
	if c.Limits.Reader.MaxEnvelopeBytes > enginekafka.MaxConsumerRecordBytes() {
		return errors.New("limits.reader.max_envelope_bytes exceeds Kafka consumer record fetch budget")
	}
	if c.Limits.Trigger.MaxEvidenceBytesPerEvent > c.Kafka.TriggerEvent.MaxMessageBytes {
		return errors.New("trigger_event max_message_bytes cannot admit maximum trigger evidence")
	}
	if c.ReceiptQueue.MaxQueuedMessages <= 0 || c.ReceiptQueue.MaxQueuedBytes <= 0 {
		return errors.New("receipt_queue budgets must be positive")
	}
	if c.ReceiptQueue.MaxQueuedBytes < c.Kafka.MessageReceipt.MaxMessageBytes {
		return errors.New("receipt_queue max_queued_bytes cannot admit one maximum message receipt")
	}
	return nil
}

func (c Config) validateSharedRuntime() error {
	if err := c.validateRedis(); err != nil {
		return err
	}
	if err := c.Limits.validate(); err != nil {
		return err
	}
	if c.DependencyRetry.MinDelay.Duration() <= 0 || c.DependencyRetry.MaxDelay.Duration() < c.DependencyRetry.MinDelay.Duration() {
		return errors.New("dependency_retry delay range is invalid")
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
	if err := c.Redis.Connection().validate("redis"); err != nil {
		return err
	}
	if !canonicalRedisPrefix(c.Redis.StatePrefix) {
		return errors.New("redis state_prefix must be non-empty canonical text")
	}
	if c.Redis.MinTTL.Duration() <= 0 || c.Redis.MaxTTL.Duration() < c.Redis.MinTTL.Duration() || c.Redis.RestartMargin.Duration() < 0 {
		return errors.New("redis state TTL range is invalid")
	}
	return nil
}
