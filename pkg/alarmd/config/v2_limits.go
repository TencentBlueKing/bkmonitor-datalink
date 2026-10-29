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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

const defaultOutputMaxMessageBytes = 512 << 10

type CompilerLimitsConfig struct {
	MaxPlanBytes              int    `yaml:"max_plan_bytes"`
	MaxLevelsPerPlan          int    `yaml:"max_levels_per_plan"`
	MaxAlgorithmsPerLevel     int    `yaml:"max_algorithms_per_level"`
	MaxGroupsPerAlgorithm     int    `yaml:"max_groups_per_algorithm"`
	MaxConditionsPerAlgorithm int    `yaml:"max_conditions_per_algorithm"`
	MaxASTNodesPerLevel       int    `yaml:"max_ast_nodes_per_level"`
	MaxRequiredHistoryPoints  uint32 `yaml:"max_required_history_points"`
	MaxCompiledPlanBytes      int    `yaml:"max_compiled_plan_bytes"`
	// MaxCacheEntries and MaxCacheBytes bound the compile cache. They are
	// sized from the container (DeriveCompileCache), not configured.
	MaxCacheEntries  int      `yaml:"-"`
	MaxCacheBytes    int      `yaml:"-"`
	NegativeCacheTTL Duration `yaml:"negative_cache_ttl"`
	BudgetRevision   string   `yaml:"budget_revision"`
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

// StoreLimitsConfig is the batch the state store applies in. Three byte
// budgets beside it -- key bytes per batch, loaded and written bytes -- were
// read by nothing but their own positivity check and are gone; what one
// Slot's state may cost is the coordinator's retained bytes and the codec's
// encoded bound.
type StoreLimitsConfig struct {
	MaxKeysPerBatch int `yaml:"max_keys_per_batch"`
}

type LimitsConfig struct {
	Compiler CompilerLimitsConfig `yaml:"compiler"`
	Trigger  TriggerLimitsConfig  `yaml:"trigger"`
	Codec    CodecLimitsConfig    `yaml:"codec"`
	Store    StoreLimitsConfig    `yaml:"store"`
}

func defaultLimits() LimitsConfig {
	return LimitsConfig{
		Compiler: CompilerLimitsConfig{
			// The shape limits bound compile CPU, which the compile cache and
			// the trigger's compute cost already bound; they only have to stop
			// a malformed Plan, not a large real one (a long IP list).
			MaxPlanBytes: 2 << 20, MaxLevelsPerPlan: 8, MaxAlgorithmsPerLevel: 64,
			MaxGroupsPerAlgorithm: 256, MaxConditionsPerAlgorithm: 1024, MaxASTNodesPerLevel: 8192,
			MaxRequiredHistoryPoints: 4096, MaxCompiledPlanBytes: 4 << 20,
			NegativeCacheTTL: Duration(time.Minute),
			BudgetRevision:   "phase-one-default-v1",
		},
		// There is no detect section any more. Five of its seven budgets went
		// with the batch evaluation that read them; the last two did not bound
		// anything real. max_plans was checked to be at least one and read
		// nowhere else. max_records_per_series, 500, refused every evaluation
		// of a Plan whose window holds more points than that, a Plan the
		// compiler had admitted; a series is now bounded by its own window
		// (evaluation.commonPrimaryRecords), and what a Slot holds in memory
		// by the coordinator's retained bytes.
		Trigger: TriggerLimitsConfig{
			MaxLevels: 8, MaxTriggerWindowSize: 2048, MaxRecoveryConsecutiveWindows: 2048,
			MaxRequiredHistoryPoints: 4096, MaxLevelResultsPerEvent: 8,
			MaxEvidenceBytesPerEvent: 64 << 10, MaxComputeCost: 1 << 20,
		},
		Codec: CodecLimitsConfig{MaxLevels: 8, MaxPoints: 4096, MaxEncodedBytes: 512 << 10},
		Store: StoreLimitsConfig{MaxKeysPerBatch: 8192},
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
		MaxRetainedPointsByLevels:     c.retainedPointCeilings(),
		MaxTriggerComputeCost:         c.Limits.Trigger.MaxComputeCost,
		MaxCompiledPlanBytes:          v.MaxCompiledPlanBytes,
		MaxCacheEntries:               v.MaxCacheEntries, MaxCacheBytes: v.MaxCacheBytes,
		NegativeCacheTTL: v.NegativeCacheTTL.Duration(), BudgetRevision: v.BudgetRevision,
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

func (c LimitsConfig) validate() error {
	compiler := c.Compiler
	if compiler.MaxPlanBytes <= 0 || compiler.MaxLevelsPerPlan <= 0 || compiler.MaxAlgorithmsPerLevel <= 0 ||
		compiler.MaxGroupsPerAlgorithm <= 0 || compiler.MaxConditionsPerAlgorithm <= 0 || compiler.MaxASTNodesPerLevel <= 0 ||
		compiler.MaxRequiredHistoryPoints == 0 || compiler.MaxCompiledPlanBytes <= 0 || compiler.MaxCacheEntries <= 0 ||
		compiler.MaxCacheBytes <= 0 || compiler.NegativeCacheTTL.Duration() <= 0 || compiler.BudgetRevision == "" ||
		strings.TrimSpace(compiler.BudgetRevision) != compiler.BudgetRevision {
		return errors.New("limits.compiler budgets must be positive and budget_revision canonical")
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
	if store.MaxKeysPerBatch <= 0 {
		return errors.New("limits.store budgets must be positive")
	}
	if compiler.MaxLevelsPerPlan > int(triggerLimits.MaxLevels) ||
		compiler.MaxLevelsPerPlan > int(triggerLimits.MaxLevelResultsPerEvent) || compiler.MaxLevelsPerPlan > codec.MaxLevels {
		return errors.New("plan and level budgets are inconsistent")
	}
	if compiler.MaxLevelsPerPlan > linkdoutput.MaxEvaluations {
		// Every decided level of a Plan is one evaluation on the standard
		// output, and the consumer refuses a message carrying more than its
		// bound. The relation lives here, where the budget is set, rather
		// than as a refusal met one alert at a time.
		return fmt.Errorf("limits.compiler.max_levels_per_plan %d exceeds the %d evaluations the standard output carries", compiler.MaxLevelsPerPlan, linkdoutput.MaxEvaluations)
	}
	if compiler.MaxRequiredHistoryPoints > triggerLimits.MaxRequiredHistoryPoints ||
		compiler.MaxRequiredHistoryPoints > uint32(codec.MaxPoints) {
		return errors.New("history point budgets are inconsistent")
	}
	if compiler.MaxCompiledPlanBytes > compiler.MaxCacheBytes {
		return errors.New("compiler cache cannot admit one maximum compiled plan")
	}
	return nil
}

// retainedPointCeilings is how many retained points fit one stored Runtime
// State record, by how many Levels share it.
//
// Derived from the representation the store writes rather than stated beside
// it. Under the JSON envelope the configured max_required_history_points of
// 4096 was unreachable - the record ran out at roughly 2100 points for one
// Level and 1400 for two - so the number in the file described a limit that
// could not be reached and the real one was written nowhere: a Plan configured
// between them compiled, activated, and then had every state write refused per
// series with nothing to say why.
//
// It follows the framed record now that the store writes one, which is the
// case this was built to handle. The ceilings rise by more than an order of
// magnitude and the configured limit becomes the binding one again: 4096
// points is reachable at every Level count, so a Plan is bounded by what it
// asked for rather than by the shape of the value it is stored in. That is
// the intended end state, not a reason to leave the derivation behind - a
// ceiling that has stopped binding is exactly a ceiling nobody notices has
// moved, and the next representation or the next configured limit puts it
// back in play with nothing else to revisit.
func (c Config) retainedPointCeilings() []uint32 {
	levels := c.Limits.Compiler.MaxLevelsPerPlan
	if levels <= 0 {
		return nil
	}
	// One past MaxLevelsPerPlan: a Plan that detects no-data compiles a Level
	// for it beside the ones the strategy declares, and that Level's facts are
	// written on the same points as the rest.
	ceilings := make([]uint32, levels+2)
	for count := 1; count < len(ceilings); count++ {
		points, err := state.MaxPackedFramePoints(count, c.Limits.Codec.MaxEncodedBytes)
		if err != nil || points <= 0 {
			continue
		}
		ceilings[count] = uint32(points)
	}
	return ceilings
}
