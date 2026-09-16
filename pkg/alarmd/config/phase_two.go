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
)

// InputMode selects the only active input source for one phase-two worker.
// Runtime dispatch is mode-specific; neither source identity enters Slot,
// State, Progress or Event contracts.
type InputMode string

const InputModeGoAccess InputMode = "go_access"

// PhaseTwoInputConfig is the input-selection schema. Go Access is the only
// mode, and the default; the field stays so that a deployment naming a mode
// this build does not have is refused by name rather than ignored.
type PhaseTwoInputConfig struct {
	Mode InputMode `yaml:"mode"`
}

func DefaultPhaseTwoInput() PhaseTwoInputConfig {
	return PhaseTwoInputConfig{Mode: InputModeGoAccess}
}

func (c PhaseTwoInputConfig) Validate() error {
	if c.Mode != InputModeGoAccess {
		return fmt.Errorf("phase-two input mode %q is not supported", c.Mode)
	}
	return nil
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
	// The two output topics carry different wire formats. Publishing both to
	// one topic puts a native event and a Python-compatible event side by side
	// on a stream whose consumer knows only one of them, and the consumer that
	// guesses wrong either drops alerts or misreads them. There was an output
	// allowlist here that could not catch this - a configuration naming one
	// topic twice passes an allowlist containing it - while everything the
	// allowlist did check was already checked from the topic itself.
	if c.LegacyAdapter.Topic != "" && c.LegacyAdapter.Topic == c.TriggerEvent.Topic {
		return fmt.Errorf(
			"kafka producer: trigger_event.topic and legacy_adapter.topic must differ, both are %q",
			c.TriggerEvent.Topic,
		)
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
