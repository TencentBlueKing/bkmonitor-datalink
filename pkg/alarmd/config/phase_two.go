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
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/Shopify/sarama"

	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
)

// InputMode selects the only active input source for one phase-two worker.
// Runtime dispatch is mode-specific; neither source identity enters Slot,
// State, Progress or Event contracts.
type InputMode string

const (
	InputModeGoAccess                   InputMode = "go_access"
	InputModePhaseOneKafkaCompatibility InputMode = "phase_one_kafka_compatibility"
)

// PhaseOneKafkaCompatibilityConfig names the phase-one input identities that
// may be used only by the explicit compatibility mode. It does not make those
// identities part of the phase-two Slot, State, Progress or Event contracts.
type PhaseOneKafkaCompatibilityConfig struct {
	InputTopic    string `yaml:"input_topic"`
	ConsumerGroup string `yaml:"consumer_group"`
	InitialOffset string `yaml:"initial_offset"`
	StatePrefix   string `yaml:"state_prefix"`
}

func (c PhaseOneKafkaCompatibilityConfig) Validate() error {
	for name, value := range map[string]string{
		"input_topic": c.InputTopic, "consumer_group": c.ConsumerGroup, "state_prefix": c.StatePrefix,
	} {
		if value == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("phase-one Kafka compatibility %s must be non-empty canonical text", name)
		}
	}
	if c.InitialOffset != enginekafka.InitialOffsetOldest && c.InitialOffset != enginekafka.InitialOffsetLatest {
		return fmt.Errorf(
			"phase-one Kafka compatibility initial_offset must be %q or %q",
			enginekafka.InitialOffsetOldest, enginekafka.InitialOffsetLatest,
		)
	}
	return nil
}

// PhaseTwoInputConfig is the input-selection schema. Go Access is the default.
// Supplying phase-one coordinates while Go Access is selected is an error
// rather than an implicit fallback to the old consumer path.
type PhaseTwoInputConfig struct {
	Mode          InputMode                         `yaml:"mode"`
	PhaseOneKafka *PhaseOneKafkaCompatibilityConfig `yaml:"phase_one_kafka,omitempty"`
}

func DefaultPhaseTwoInput() PhaseTwoInputConfig {
	return PhaseTwoInputConfig{Mode: InputModeGoAccess}
}

func (c PhaseTwoInputConfig) Validate() error {
	switch c.Mode {
	case InputModeGoAccess:
		if c.PhaseOneKafka != nil {
			return errors.New("phase-two Go Access cannot be enabled with phase-one Kafka compatibility")
		}
		return nil
	case InputModePhaseOneKafkaCompatibility:
		if c.PhaseOneKafka == nil {
			return errors.New("phase-one Kafka compatibility requires explicit coordinates")
		}
		return c.PhaseOneKafka.Validate()
	default:
		return fmt.Errorf("phase-two input mode %q is not supported", c.Mode)
	}
}

// PhaseOneCompatibilityRuntimeConfig maps the explicitly isolated
// compatibility coordinates into the phase-one runtime fields. The default Go
// Access path never calls this conversion and therefore cannot silently fall
// back to the phase-one consumer.
func (c Config) PhaseOneCompatibilityRuntimeConfig() (Config, error) {
	if c.Input.Mode != InputModePhaseOneKafkaCompatibility || c.Input.PhaseOneKafka == nil {
		return Config{}, errors.New("phase-one runtime requires explicit Kafka compatibility mode")
	}
	compatibility := *c.Input.PhaseOneKafka
	if err := compatibility.Validate(); err != nil {
		return Config{}, err
	}
	for name, values := range map[string][2]string{
		"kafka.input_topic":    {c.Kafka.InputTopic, compatibility.InputTopic},
		"kafka.group_id":       {c.Kafka.GroupID, compatibility.ConsumerGroup},
		"kafka.initial_offset": {c.Kafka.InitialOffset, compatibility.InitialOffset},
		"redis.state_prefix":   {c.Redis.StatePrefix, compatibility.StatePrefix},
	} {
		if values[0] != "" && values[0] != values[1] {
			return Config{}, fmt.Errorf("%s conflicts with explicit phase-one compatibility coordinates", name)
		}
	}

	runtimeConfig := c
	runtimeConfig.Kafka.InputTopic = compatibility.InputTopic
	runtimeConfig.Kafka.GroupID = compatibility.ConsumerGroup
	runtimeConfig.Kafka.InitialOffset = compatibility.InitialOffset
	runtimeConfig.Redis.StatePrefix = compatibility.StatePrefix
	return runtimeConfig, nil
}

var phaseTwoKafkaTopicNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// validatePhaseTwoKafkaOutput validates only the TriggerEvent producer
// topology. Reusing DecisionSinkConfig.Validate would incorrectly require a
// phase-one input topic on the default Go Access path.
func validatePhaseTwoKafkaOutput(c KafkaConfig) error {
	if len(c.Brokers) == 0 {
		return errors.New("kafka producer: at least one broker is required")
	}
	seenBrokers := make(map[string]struct{}, len(c.Brokers))
	for _, broker := range c.Brokers {
		if err := validatePhaseTwoBroker(broker); err != nil {
			return err
		}
		if _, exists := seenBrokers[broker]; exists {
			return fmt.Errorf("kafka producer: duplicate broker %q", broker)
		}
		seenBrokers[broker] = struct{}{}
	}
	if err := validatePhaseTwoTopic("trigger_event.topic", c.TriggerEvent.Topic); err != nil {
		return err
	}
	if len(c.AllowedOutputTopics) == 0 {
		return errors.New("kafka producer: output topic allowlist must be non-empty")
	}
	seenTopics := make(map[string]struct{}, len(c.AllowedOutputTopics))
	for _, topic := range c.AllowedOutputTopics {
		if err := validatePhaseTwoTopic("allowed_output_topics", topic); err != nil {
			return err
		}
		if _, exists := seenTopics[topic]; exists {
			return fmt.Errorf("kafka producer: duplicate allowed output topic %q", topic)
		}
		seenTopics[topic] = struct{}{}
	}
	if _, allowed := seenTopics[c.TriggerEvent.Topic]; !allowed {
		return fmt.Errorf("kafka producer: output topic %q is not allowlisted", c.TriggerEvent.Topic)
	}
	for name, value := range map[string]string{"client_id": c.ClientID, "broker_version": c.BrokerVersion} {
		if value == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("kafka producer: %s must be non-empty canonical text", name)
		}
	}
	if c.TriggerEvent.MaxMessageBytes <= 0 {
		return errors.New("kafka producer: trigger_event.max_message_bytes must be positive")
	}
	version, err := sarama.ParseKafkaVersion(c.BrokerVersion)
	if err != nil {
		return fmt.Errorf("kafka producer: broker_version %q: %w", c.BrokerVersion, err)
	}
	if !version.IsAtLeast(sarama.V0_10_2_0) || !sarama.MaxVersion.IsAtLeast(version) {
		return fmt.Errorf(
			"kafka producer: broker_version %q is outside supported range 0.10.2.0..%s",
			c.BrokerVersion, sarama.MaxVersion,
		)
	}
	return nil
}

func validatePhaseTwoBroker(broker string) error {
	if broker == "" || strings.TrimSpace(broker) != broker {
		return fmt.Errorf("kafka producer: broker %q must be canonical host:port", broker)
	}
	host, port, err := net.SplitHostPort(broker)
	if err != nil {
		return fmt.Errorf("kafka producer: broker %q: %w", broker, err)
	}
	portNumber, parseErr := strconv.Atoi(port)
	if host == "" || parseErr != nil || portNumber <= 0 || portNumber > 65535 {
		return fmt.Errorf("kafka producer: broker %q has invalid host or port", broker)
	}
	return nil
}

func validatePhaseTwoTopic(field, topic string) error {
	if topic == "" || strings.TrimSpace(topic) != topic {
		return fmt.Errorf("kafka producer: %s must be non-empty canonical text", field)
	}
	if len(topic) > 249 || topic == "." || topic == ".." || !phaseTwoKafkaTopicNamePattern.MatchString(topic) {
		return fmt.Errorf("kafka producer: %s %q is not a valid Kafka topic name", field, topic)
	}
	return nil
}

type PhaseOneAsset string

const (
	PhaseOneInputTopic              PhaseOneAsset = "phase_one_input_topic"
	PhaseOneConsumerGroup           PhaseOneAsset = "phase_one_consumer_group"
	PhaseOneInitialOffset           PhaseOneAsset = "phase_one_initial_offset"
	PhaseOneInputMetrics            PhaseOneAsset = "phase_one_input_metrics"
	PhaseOneStatePrefix             PhaseOneAsset = "phase_one_state_prefix"
	SharedResourceEvaluationMetrics PhaseOneAsset = "shared_resource_evaluation_metrics"
)

type AssetDisposition string

const (
	AssetReuse             AssetDisposition = "REUSE"
	AssetCompatibilityOnly AssetDisposition = "COMPATIBILITY_ONLY"
	AssetDisabled          AssetDisposition = "DISABLED"
	AssetExit              AssetDisposition = "EXIT"
)

// PhaseOneAssetPolicy freezes which phase-one assets remain visible during
// compatibility, which are allowed on the default Go Access path, and which
// must have left that path by G5. Input metrics include consumer/claim/offset,
// Adapter and MessageReceipt views; resource and Evaluation work metrics keep
// their established units but do not retain phase-one input semantics.
type PhaseOneAssetPolicy struct {
	Asset         PhaseOneAsset
	Compatibility AssetDisposition
	GoAccess      AssetDisposition
	G5            AssetDisposition
}

var phaseOneAssetPolicies = [...]PhaseOneAssetPolicy{
	{Asset: PhaseOneInputTopic, Compatibility: AssetCompatibilityOnly, GoAccess: AssetDisabled, G5: AssetExit},
	{Asset: PhaseOneConsumerGroup, Compatibility: AssetCompatibilityOnly, GoAccess: AssetDisabled, G5: AssetExit},
	{Asset: PhaseOneInitialOffset, Compatibility: AssetCompatibilityOnly, GoAccess: AssetDisabled, G5: AssetExit},
	{Asset: PhaseOneInputMetrics, Compatibility: AssetCompatibilityOnly, GoAccess: AssetDisabled, G5: AssetExit},
	{Asset: PhaseOneStatePrefix, Compatibility: AssetCompatibilityOnly, GoAccess: AssetDisabled, G5: AssetExit},
	{Asset: SharedResourceEvaluationMetrics, Compatibility: AssetReuse, GoAccess: AssetReuse, G5: AssetReuse},
}

func PhaseOneAssetPolicies() []PhaseOneAssetPolicy {
	return append([]PhaseOneAssetPolicy(nil), phaseOneAssetPolicies[:]...)
}
