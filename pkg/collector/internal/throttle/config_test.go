// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package throttle

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestBuildRules(t *testing.T) {
	enabled := false
	dropMin := 0.1
	dropMax := 0.6
	metricsDropMax := 0.0

	rules := buildRules(map[string]RuleConfig{
		"default": {
			DropMin: &dropMin,
			DropMax: &dropMax,
		},
		define.RecordMetrics.S(): {
			Enabled: &enabled,
			DropMax: &metricsDropMax,
		},
	})

	assert.Equal(t, Rule{Enabled: true, DropMin: 0.1, DropMax: 0.6}, rules[define.RecordTraces])
	assert.Equal(t, Rule{Enabled: false, DropMin: 0.1, DropMax: 0.0}, rules[define.RecordMetrics])
}

func TestValidateConfig(t *testing.T) {
	config := normalizeConfig(Config{Enabled: true})
	assert.NoError(t, validateConfig(config))

	config.Rules = map[string]RuleConfig{"unknown": {}}
	assert.Error(t, validateConfig(config))

	dropMin := 0.5
	dropMax := 0.0
	config.Rules = map[string]RuleConfig{
		"default": {
			DropMin: &dropMin,
		},
		define.RecordMetrics.S(): {
			DropMax: &dropMax,
		},
	}
	assert.Error(t, validateConfig(config))
}

func TestValidateConfigMemThresholds(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*ThresholdSlotConfig)
		expected bool // true 表示应通过
	}{
		{
			name:     "default mem thresholds pass",
			mutate:   func(*ThresholdSlotConfig) {},
			expected: true,
		},
		{
			name:     "mem_enter <= mem_exit rejected",
			mutate:   func(t *ThresholdSlotConfig) { t.Enter, t.Exit = 0.78, 0.85 },
			expected: false,
		},
		{
			name:     "mem_hard <= mem_enter rejected",
			mutate:   func(t *ThresholdSlotConfig) { t.Enter, t.Hard = 0.92, 0.85 },
			expected: false,
		},
		{
			name:     "negative mem_exit rejected",
			mutate:   func(t *ThresholdSlotConfig) { t.Exit = -0.1 },
			expected: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := normalizeConfig(Config{Enabled: true})
			tc.mutate(&config.Thresholds.Mem)
			err := validateConfig(config)
			if tc.expected {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestNormalizeConfigMemDefaults(t *testing.T) {
	config := normalizeConfig(Config{Enabled: true})
	assert.True(t, thresholdEnabled(config.Thresholds.CPU))
	assert.True(t, thresholdEnabled(config.Thresholds.Mem))
	assert.Equal(t, defaultMemEnter, config.Thresholds.Mem.Enter)
	assert.Equal(t, defaultMemExit, config.Thresholds.Mem.Exit)
	assert.Equal(t, defaultMemHard, config.Thresholds.Mem.Hard)
	assert.Equal(t, defaultMemBreachN, config.Thresholds.Mem.BreachN)
	assert.Less(t, config.Thresholds.Mem.Exit, config.Thresholds.Mem.Enter)
	assert.Less(t, config.Thresholds.Mem.Enter, config.Thresholds.Mem.Hard)
}

func TestValidateConfigDisabledThresholdSlots(t *testing.T) {
	disabled := false

	tests := []struct {
		name   string
		mutate func(*ThresholdConfig)
	}{
		{
			name: "cpu disabled",
			mutate: func(thresholds *ThresholdConfig) {
				thresholds.CPU = ThresholdSlotConfig{
					Enabled: &disabled,
					Enter:   0.7,
					Exit:    0.8,
					Hard:    0.6,
					BreachN: 0,
				}
			},
		},
		{
			name: "mem disabled",
			mutate: func(thresholds *ThresholdConfig) {
				thresholds.Mem = ThresholdSlotConfig{
					Enabled: &disabled,
					Enter:   0.7,
					Exit:    0.8,
					Hard:    0.6,
					BreachN: 0,
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := normalizeConfig(Config{Enabled: true})
			tc.mutate(&config.Thresholds)
			assert.NoError(t, validateConfig(config))
		})
	}
}
