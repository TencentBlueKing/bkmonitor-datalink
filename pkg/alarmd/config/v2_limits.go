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
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

const defaultOutputMaxMessageBytes = 512 << 10

type ReaderLimitsConfig struct {
	MaxEnvelopeBytes     int `yaml:"max_envelope_bytes"`
	MaxRecordsPerMessage int `yaml:"max_records_per_message"`
	MaxPlansPerMessage   int `yaml:"max_plans_per_message"`
	MaxLevelsPerPlan     int `yaml:"max_levels_per_plan"`
	MaxSelectorBytes     int `yaml:"max_selector_bytes"`
	MaxRecordBytes       int `yaml:"max_record_bytes"`
	MaxPlanSetBytes      int `yaml:"max_plan_set_bytes"`
	MaxContractDepth     int `yaml:"max_contract_depth"`
	MaxStringBytes       int `yaml:"max_string_bytes"`
	MaxValidationIssues  int `yaml:"max_validation_issues"`
}

type CompilerLimitsConfig struct {
	MaxPlanBytes              int      `yaml:"max_plan_bytes"`
	MaxLevelsPerPlan          int      `yaml:"max_levels_per_plan"`
	MaxAlgorithmsPerLevel     int      `yaml:"max_algorithms_per_level"`
	MaxGroupsPerAlgorithm     int      `yaml:"max_groups_per_algorithm"`
	MaxConditionsPerAlgorithm int      `yaml:"max_conditions_per_algorithm"`
	MaxASTNodesPerLevel       int      `yaml:"max_ast_nodes_per_level"`
	MaxRequiredHistoryPoints  uint32   `yaml:"max_required_history_points"`
	MaxCompiledPlanBytes      int      `yaml:"max_compiled_plan_bytes"`
	MaxCacheEntries           int      `yaml:"max_cache_entries"`
	MaxCacheBytes             int      `yaml:"max_cache_bytes"`
	NegativeCacheTTL          Duration `yaml:"negative_cache_ttl"`
	BudgetRevision            string   `yaml:"budget_revision"`
}

type DetectLimitsConfig struct {
	MaxPlans                  uint64 `yaml:"max_plans"`
	MaxSelectedRecordsPerPlan uint64 `yaml:"max_selected_records_per_plan"`
	MaxSeriesPerPlan          uint64 `yaml:"max_series_per_plan"`
	MaxRecordsPerSeries       uint64 `yaml:"max_records_per_series"`
	MaxLevelFacts             uint64 `yaml:"max_level_facts"`
	MaxPredicateEvaluations   uint64 `yaml:"max_predicate_evaluations"`
	MaxResultBytes            uint64 `yaml:"max_result_bytes"`
}

type TriggerLimitsConfig struct {
	MaxLevels                     uint32 `yaml:"max_levels"`
	MaxTriggerWindowSize          uint32 `yaml:"max_trigger_window_size"`
	MaxRecoveryConsecutiveWindows uint32 `yaml:"max_recovery_consecutive_windows"`
	MaxRequiredHistoryPoints      uint32 `yaml:"max_required_history_points"`
	MaxLevelResultsPerEvent       uint32 `yaml:"max_level_results_per_event"`
	MaxEvidenceBytesPerEvent      int    `yaml:"max_evidence_bytes_per_event"`
	MaxComputeCost                uint64 `yaml:"max_compute_cost"`
}

type CodecLimitsConfig struct {
	MaxLevels       int `yaml:"max_levels"`
	MaxPoints       int `yaml:"max_points"`
	MaxEncodedBytes int `yaml:"max_encoded_bytes"`
}

type StoreLimitsConfig struct {
	MaxKeysPerBatch     int `yaml:"max_keys_per_batch"`
	MaxKeyBytesPerBatch int `yaml:"max_key_bytes_per_batch"`
	MaxLoadedBytes      int `yaml:"max_loaded_bytes"`
	MaxWrittenBytes     int `yaml:"max_written_bytes"`
}

type LimitsConfig struct {
	Reader   ReaderLimitsConfig   `yaml:"reader"`
	Compiler CompilerLimitsConfig `yaml:"compiler"`
	Detect   DetectLimitsConfig   `yaml:"detect"`
	Trigger  TriggerLimitsConfig  `yaml:"trigger"`
	Codec    CodecLimitsConfig    `yaml:"codec"`
	Store    StoreLimitsConfig    `yaml:"store"`
}

func defaultLimits() LimitsConfig {
	return LimitsConfig{
		Reader: ReaderLimitsConfig{
			MaxEnvelopeBytes: 512 << 10, MaxRecordsPerMessage: 500, MaxPlansPerMessage: 16, MaxLevelsPerPlan: 8,
			MaxSelectorBytes: 64 << 10, MaxRecordBytes: 128 << 10, MaxPlanSetBytes: 256 << 10,
			MaxContractDepth: 32, MaxStringBytes: 64 << 10, MaxValidationIssues: 256,
		},
		Compiler: CompilerLimitsConfig{
			MaxPlanBytes: 256 << 10, MaxLevelsPerPlan: 8, MaxAlgorithmsPerLevel: 8,
			MaxGroupsPerAlgorithm: 16, MaxConditionsPerAlgorithm: 32, MaxASTNodesPerLevel: 512,
			MaxRequiredHistoryPoints: 4096, MaxCompiledPlanBytes: 512 << 10,
			MaxCacheEntries: 4096, MaxCacheBytes: 64 << 20, NegativeCacheTTL: Duration(time.Minute),
			BudgetRevision: "phase-one-default-v1",
		},
		Detect: DetectLimitsConfig{
			MaxPlans: 16, MaxSelectedRecordsPerPlan: 500, MaxSeriesPerPlan: 500, MaxRecordsPerSeries: 500,
			MaxLevelFacts: 64000, MaxPredicateEvaluations: 512000, MaxResultBytes: 16 << 20,
		},
		Trigger: TriggerLimitsConfig{
			MaxLevels: 8, MaxTriggerWindowSize: 2048, MaxRecoveryConsecutiveWindows: 2048,
			MaxRequiredHistoryPoints: 4096, MaxLevelResultsPerEvent: 8,
			MaxEvidenceBytesPerEvent: 64 << 10, MaxComputeCost: 1 << 20,
		},
		Codec: CodecLimitsConfig{MaxLevels: 8, MaxPoints: 4096, MaxEncodedBytes: 512 << 10},
		Store: StoreLimitsConfig{
			MaxKeysPerBatch: 8192, MaxKeyBytesPerBatch: 2 << 20,
			MaxLoadedBytes: 64 << 20, MaxWrittenBytes: 64 << 20,
		},
	}
}

func (c Config) ReaderLimits() contract.ReaderLimitsV2 {
	v := c.Limits.Reader
	return contract.ReaderLimitsV2{
		MaxEnvelopeBytes: v.MaxEnvelopeBytes, MaxRecordsPerMessage: v.MaxRecordsPerMessage,
		MaxPlansPerMessage: v.MaxPlansPerMessage, MaxLevelsPerPlan: v.MaxLevelsPerPlan,
		MaxSelectorBytes: v.MaxSelectorBytes, MaxRecordBytes: v.MaxRecordBytes, MaxPlanSetBytes: v.MaxPlanSetBytes,
		MaxContractDepth: v.MaxContractDepth, MaxStringBytes: v.MaxStringBytes, MaxValidationIssues: v.MaxValidationIssues,
	}
}

func (c Config) CompilerLimits() strategy.Limits {
	v := c.Limits.Compiler
	return strategy.Limits{
		MaxPlanBytes: v.MaxPlanBytes, MaxLevelsPerPlan: v.MaxLevelsPerPlan,
		MaxAlgorithmsPerLevel: v.MaxAlgorithmsPerLevel, MaxGroupsPerAlgorithm: v.MaxGroupsPerAlgorithm,
		MaxConditionsPerAlgorithm: v.MaxConditionsPerAlgorithm, MaxASTNodesPerLevel: v.MaxASTNodesPerLevel,
		MaxTriggerWindowSize:          c.Limits.Trigger.MaxTriggerWindowSize,
		MaxRecoveryConsecutiveWindows: c.Limits.Trigger.MaxRecoveryConsecutiveWindows,
		MaxRequiredHistoryPoints:      v.MaxRequiredHistoryPoints,
		MaxTriggerComputeCost:         c.Limits.Trigger.MaxComputeCost,
		MaxCompiledPlanBytes:          v.MaxCompiledPlanBytes,
		MaxCacheEntries:               v.MaxCacheEntries, MaxCacheBytes: v.MaxCacheBytes,
		NegativeCacheTTL: v.NegativeCacheTTL.Duration(), BudgetRevision: v.BudgetRevision,
	}
}

func (c Config) DetectLimits() detect.ExecutionLimits {
	v := c.Limits.Detect
	return detect.ExecutionLimits{
		MaxPlans: v.MaxPlans, MaxSelectedRecordsPerPlan: v.MaxSelectedRecordsPerPlan,
		MaxSeriesPerPlan: v.MaxSeriesPerPlan, MaxRecordsPerSeries: v.MaxRecordsPerSeries,
		MaxLevelFacts: v.MaxLevelFacts, MaxPredicateEvaluations: v.MaxPredicateEvaluations,
		MaxResultBytes: v.MaxResultBytes,
	}
}

func (c Config) TriggerLimits() trigger.EvaluationLimitsV2 {
	v := c.Limits.Trigger
	return trigger.EvaluationLimitsV2{
		MaxLevels: v.MaxLevels, MaxTriggerWindowSize: v.MaxTriggerWindowSize,
		MaxRecoveryConsecutiveWindows: v.MaxRecoveryConsecutiveWindows,
		MaxRequiredHistoryPoints:      v.MaxRequiredHistoryPoints, MaxLevelResultsPerEvent: v.MaxLevelResultsPerEvent,
		MaxEvidenceBytesPerEvent: v.MaxEvidenceBytesPerEvent, MaxComputeCost: v.MaxComputeCost,
	}
}

func (c Config) CodecLimits() state.CodecLimits {
	v := c.Limits.Codec
	return state.CodecLimits{MaxLevels: v.MaxLevels, MaxPoints: v.MaxPoints, MaxEncodedBytes: v.MaxEncodedBytes}
}

func (c Config) StoreLimits() state.StoreLimits {
	v := c.Limits.Store
	return state.StoreLimits{
		MaxKeysPerBatch: v.MaxKeysPerBatch, MaxKeyBytesPerBatch: v.MaxKeyBytesPerBatch,
		MaxLoadedBytes: v.MaxLoadedBytes, MaxWrittenBytes: v.MaxWrittenBytes,
	}
}

func (c LimitsConfig) validate() error {
	r := c.Reader
	if r.MaxEnvelopeBytes <= 0 || r.MaxRecordsPerMessage <= 0 || r.MaxPlansPerMessage <= 0 || r.MaxLevelsPerPlan <= 0 ||
		r.MaxSelectorBytes <= 0 || r.MaxRecordBytes <= 0 || r.MaxPlanSetBytes <= 0 || r.MaxContractDepth <= 0 ||
		r.MaxStringBytes <= 0 || r.MaxValidationIssues <= 0 {
		return errors.New("limits.reader budgets must be positive")
	}
	compiler := c.Compiler
	if compiler.MaxPlanBytes <= 0 || compiler.MaxLevelsPerPlan <= 0 || compiler.MaxAlgorithmsPerLevel <= 0 ||
		compiler.MaxGroupsPerAlgorithm <= 0 || compiler.MaxConditionsPerAlgorithm <= 0 || compiler.MaxASTNodesPerLevel <= 0 ||
		compiler.MaxRequiredHistoryPoints == 0 || compiler.MaxCompiledPlanBytes <= 0 || compiler.MaxCacheEntries <= 0 ||
		compiler.MaxCacheBytes <= 0 || compiler.NegativeCacheTTL.Duration() <= 0 || compiler.BudgetRevision == "" ||
		strings.TrimSpace(compiler.BudgetRevision) != compiler.BudgetRevision {
		return errors.New("limits.compiler budgets must be positive and budget_revision canonical")
	}
	detectLimits := c.Detect
	if detectLimits.MaxPlans == 0 || detectLimits.MaxSelectedRecordsPerPlan == 0 || detectLimits.MaxSeriesPerPlan == 0 ||
		detectLimits.MaxRecordsPerSeries == 0 || detectLimits.MaxLevelFacts == 0 || detectLimits.MaxPredicateEvaluations == 0 ||
		detectLimits.MaxResultBytes == 0 {
		return errors.New("limits.detect budgets must be positive")
	}
	triggerLimits := c.Trigger
	if triggerLimits.MaxLevels == 0 || triggerLimits.MaxTriggerWindowSize == 0 || triggerLimits.MaxRecoveryConsecutiveWindows == 0 ||
		triggerLimits.MaxRequiredHistoryPoints == 0 || triggerLimits.MaxLevelResultsPerEvent == 0 ||
		triggerLimits.MaxEvidenceBytesPerEvent <= 0 || triggerLimits.MaxComputeCost == 0 {
		return errors.New("limits.trigger budgets must be positive")
	}
	codec := c.Codec
	if codec.MaxLevels <= 0 || codec.MaxPoints <= 0 || codec.MaxEncodedBytes <= 0 {
		return errors.New("limits.codec budgets must be positive")
	}
	store := c.Store
	if store.MaxKeysPerBatch <= 0 || store.MaxKeyBytesPerBatch <= 0 || store.MaxLoadedBytes <= 0 || store.MaxWrittenBytes <= 0 {
		return errors.New("limits.store budgets must be positive")
	}
	if r.MaxSelectorBytes > r.MaxEnvelopeBytes || r.MaxRecordBytes > r.MaxEnvelopeBytes || r.MaxPlanSetBytes > r.MaxEnvelopeBytes ||
		compiler.MaxPlanBytes > r.MaxPlanSetBytes {
		return errors.New("reader and compiler byte budgets are inconsistent")
	}
	if r.MaxPlansPerMessage > int(detectLimits.MaxPlans) || r.MaxLevelsPerPlan > compiler.MaxLevelsPerPlan ||
		compiler.MaxLevelsPerPlan > int(triggerLimits.MaxLevels) ||
		compiler.MaxLevelsPerPlan > int(triggerLimits.MaxLevelResultsPerEvent) || compiler.MaxLevelsPerPlan > codec.MaxLevels {
		return errors.New("plan and level budgets are inconsistent")
	}
	if compiler.MaxRequiredHistoryPoints > triggerLimits.MaxRequiredHistoryPoints ||
		compiler.MaxRequiredHistoryPoints > uint32(codec.MaxPoints) {
		return errors.New("history point budgets are inconsistent")
	}
	if compiler.MaxCompiledPlanBytes > compiler.MaxCacheBytes {
		return errors.New("compiler cache cannot admit one maximum compiled plan")
	}
	if codec.MaxEncodedBytes > store.MaxLoadedBytes || codec.MaxEncodedBytes > store.MaxWrittenBytes {
		return errors.New("state store cannot admit one maximum encoded window")
	}
	return nil
}
