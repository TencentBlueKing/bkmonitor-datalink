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
	"strings"
	"testing"
	"time"

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

func TestDefaultPhaseTwoRuntimeHasBoundedLifecycleBudgets(t *testing.T) {
	cfg := Default().PhaseTwo

	if cfg.Control.RefreshInterval.Duration() <= 0 || cfg.Control.ReconcileInterval.Duration() <= 0 ||
		cfg.Scheduler.TickInterval.Duration() <= 0 || cfg.Access.MinReadyDelay.Duration() <= 0 ||
		cfg.Access.DownstreamExecutionReserve.Duration() <= 0 {
		t.Fatalf("phase-two cadence defaults = %+v, want positive values", cfg)
	}
	for name, ttlAndRenew := range map[string][2]time.Duration{
		"registration": {cfg.Worker.RegistrationTTL.Duration(), cfg.Worker.RegistrationRenewInterval.Duration()},
		"leader":       {cfg.Ownership.ControlLeaderTTL.Duration(), cfg.Ownership.ControlLeaderRenewInterval.Duration()},
		"query group":  {cfg.Ownership.LeaseTTL.Duration(), cfg.Ownership.LeaseRenewInterval.Duration()},
	} {
		if ttlAndRenew[0] <= ttlAndRenew[1] || ttlAndRenew[1] <= 0 {
			t.Fatalf("%s ttl/renew = %v/%v, want ttl > renew > 0", name, ttlAndRenew[0], ttlAndRenew[1])
		}
	}
	if cfg.Coordinator.MaxSequencerReservations <= 0 || cfg.Coordinator.MaxSeries == 0 ||
		cfg.Coordinator.MaxRetainedBytes == 0 || cfg.Coordinator.MaxStateMutations == 0 ||
		cfg.Coordinator.MaxEvents == 0 || cfg.Coordinator.MaxGapMutations == 0 {
		t.Fatalf("phase-two Coordinator budgets = %+v, want positive values", cfg.Coordinator)
	}
}

func TestGoAccessRequiresCompletePhaseTwoProductionCoordinates(t *testing.T) {
	valid := validGoAccessConfigObject()
	accessBKData := false
	valid.PhaseTwo.Worker.ID = "alarmd-worker-0"
	valid.PhaseTwo.Worker.DeploymentProfile = "shadow"
	valid.PhaseTwo.Control.StrategyCachePrefix = "alarm-config"
	valid.PhaseTwo.Access.UQEndpoint = "http://unify-query.service"
	valid.PhaseTwo.Access.QuerySource = "alarmd"
	valid.PhaseTwo.Control.ProviderRoute = "unify-query-primary"
	valid.PhaseTwo.Control.Timezone = "Asia/Shanghai"
	valid.PhaseTwo.Control.LegacyQueryRuntime.AccessBKData = &accessBKData
	valid.PhaseTwo.Control.LegacyQueryRuntime.BKDataCMDBLevelTables = []string{}
	valid.PhaseTwo.Control.LegacyQueryRuntime.SystemDiskFilter = PhaseTwoRuntimeFilterConfig{
		FieldName: "device_type", Values: []string{},
	}
	valid.PhaseTwo.Control.LegacyQueryRuntime.SystemNetworkFilter = PhaseTwoRuntimeFilterConfig{
		FieldName: "device_name", Values: []string{},
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("complete phase-two production configuration rejected: %v", err)
	}

	for name, mutate := range map[string]func(*Config){
		"worker identity":    func(cfg *Config) { cfg.PhaseTwo.Worker.ID = "" },
		"deployment profile": func(cfg *Config) { cfg.PhaseTwo.Worker.DeploymentProfile = "" },
		"strategy cache":     func(cfg *Config) { cfg.PhaseTwo.Control.StrategyCachePrefix = "" },
		"UQ endpoint":        func(cfg *Config) { cfg.PhaseTwo.Access.UQEndpoint = "" },
		"query source":       func(cfg *Config) { cfg.PhaseTwo.Access.QuerySource = "" },
		"downstream reserve": func(cfg *Config) { cfg.PhaseTwo.Access.DownstreamExecutionReserve = 0 },
		"provider route":     func(cfg *Config) { cfg.PhaseTwo.Control.ProviderRoute = "" },
		"timezone":           func(cfg *Config) { cfg.PhaseTwo.Control.Timezone = "" },
		"access bkdata fact": func(cfg *Config) { cfg.PhaseTwo.Control.LegacyQueryRuntime.AccessBKData = nil },
		"cmdb level tables fact": func(cfg *Config) {
			cfg.PhaseTwo.Control.LegacyQueryRuntime.BKDataCMDBLevelTables = nil
		},
		"system disk filter fact": func(cfg *Config) {
			cfg.PhaseTwo.Control.LegacyQueryRuntime.SystemDiskFilter.Values = nil
		},
		"system network filter fact": func(cfg *Config) {
			cfg.PhaseTwo.Control.LegacyQueryRuntime.SystemNetworkFilter.Values = nil
		},
		"registration cadence": func(cfg *Config) {
			cfg.PhaseTwo.Worker.RegistrationRenewInterval = cfg.PhaseTwo.Worker.RegistrationTTL
		},
		"leader cadence": func(cfg *Config) {
			cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = cfg.PhaseTwo.Ownership.ControlLeaderTTL
		},
		"lease cadence": func(cfg *Config) {
			cfg.PhaseTwo.Ownership.LeaseRenewInterval = cfg.PhaseTwo.Ownership.LeaseTTL
		},
		"state mutation store budget": func(cfg *Config) {
			cfg.PhaseTwo.Coordinator.MaxStateMutations = uint64(cfg.Limits.Store.MaxKeysPerBatch) + 1
		},
		"gap mutation store budget": func(cfg *Config) {
			cfg.PhaseTwo.Coordinator.MaxGapMutations = uint64(cfg.Limits.Store.MaxKeysPerBatch) + 1
		},
		"provider retained overflow": func(cfg *Config) {
			cfg.PhaseTwo.Coordinator.MaxRetainedBytes = ^uint64(0)
		},
		"provider record overflow": func(cfg *Config) {
			cfg.PhaseTwo.Coordinator.MaxSeries = ^uint64(0)
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			mutate(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "phase_two") {
				t.Fatalf("Validate() error = %v, want phase_two rejection", err)
			}
		})
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
