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
	"reflect"
	"testing"

	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
)

func TestDefaultPhaseTwoInputUsesGoAccessWithoutPhaseOneCoordinates(t *testing.T) {
	cfg := DefaultPhaseTwoInput()
	if cfg.Mode != InputModeGoAccess || cfg.PhaseOneKafka != nil {
		t.Fatalf("default phase-two input = %+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestPhaseTwoInputRequiresExplicitIsolatedPhaseOneCompatibility(t *testing.T) {
	compatibility := PhaseOneKafkaCompatibilityConfig{
		InputTopic: "alarmd-detect-input-shadow-v2", ConsumerGroup: "alarmd-shadow-v2",
		InitialOffset: enginekafka.InitialOffsetLatest, StatePrefix: "alarmd-shadow-v2",
	}
	for name, cfg := range map[string]PhaseTwoInputConfig{
		"go access with compatibility coordinates": {
			Mode: InputModeGoAccess, PhaseOneKafka: &compatibility,
		},
		"compatibility without coordinates": {Mode: InputModePhaseOneKafkaCompatibility},
		"unknown mode":                      {Mode: "kafka"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate() accepted %+v", cfg)
			}
		})
	}

	cfg := PhaseTwoInputConfig{Mode: InputModePhaseOneKafkaCompatibility, PhaseOneKafka: &compatibility}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected explicit compatibility: %v", err)
	}
}

func TestPhaseOneCompatibilityRejectsIncompleteOrNonCanonicalIdentity(t *testing.T) {
	valid := PhaseOneKafkaCompatibilityConfig{
		InputTopic: "alarmd-detect-input-shadow-v2", ConsumerGroup: "alarmd-shadow-v2",
		InitialOffset: enginekafka.InitialOffsetOldest, StatePrefix: "alarmd-shadow-v2",
	}
	for name, mutate := range map[string]func(*PhaseOneKafkaCompatibilityConfig){
		"input topic":    func(cfg *PhaseOneKafkaCompatibilityConfig) { cfg.InputTopic = " input" },
		"consumer group": func(cfg *PhaseOneKafkaCompatibilityConfig) { cfg.ConsumerGroup = "" },
		"initial offset": func(cfg *PhaseOneKafkaCompatibilityConfig) { cfg.InitialOffset = "earliest" },
		"state prefix":   func(cfg *PhaseOneKafkaCompatibilityConfig) { cfg.StatePrefix = "state " },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate() accepted %+v", cfg)
			}
		})
	}
}

func TestPhaseOneAssetPoliciesFreezeReuseCompatibilityAndExit(t *testing.T) {
	want := []PhaseOneAssetPolicy{
		{Asset: PhaseOneInputTopic, Compatibility: AssetCompatibilityOnly, GoAccess: AssetDisabled, G5: AssetExit},
		{Asset: PhaseOneConsumerGroup, Compatibility: AssetCompatibilityOnly, GoAccess: AssetDisabled, G5: AssetExit},
		{Asset: PhaseOneInitialOffset, Compatibility: AssetCompatibilityOnly, GoAccess: AssetDisabled, G5: AssetExit},
		{Asset: PhaseOneInputMetrics, Compatibility: AssetCompatibilityOnly, GoAccess: AssetDisabled, G5: AssetExit},
		{Asset: PhaseOneStatePrefix, Compatibility: AssetCompatibilityOnly, GoAccess: AssetDisabled, G5: AssetExit},
		{Asset: SharedResourceEvaluationMetrics, Compatibility: AssetReuse, GoAccess: AssetReuse, G5: AssetReuse},
	}
	if got := PhaseOneAssetPolicies(); !reflect.DeepEqual(got, want) {
		t.Fatalf("PhaseOneAssetPolicies() = %#v, want %#v", got, want)
	}

	got := PhaseOneAssetPolicies()
	got[0].G5 = AssetReuse
	if reflect.DeepEqual(PhaseOneAssetPolicies(), got) {
		t.Fatal("PhaseOneAssetPolicies() exposed mutable package state")
	}
}
