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
	"strings"

	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
)

// InputMode selects the only active input source for one phase-two worker.
// The C0 contract deliberately does not wire either source into a runtime.
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

// PhaseTwoInputConfig is the C0 input-selection schema. Go Access is the
// default. Supplying phase-one coordinates while Go Access is selected is an
// error rather than an implicit fallback to the old consumer path.
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
