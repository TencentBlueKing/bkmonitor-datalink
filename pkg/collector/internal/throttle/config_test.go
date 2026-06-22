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
		mutate   func(*ThresholdConfig)
		expected bool // true 表示应通过
	}{
		{
			name:     "default mem thresholds pass",
			mutate:   func(*ThresholdConfig) {},
			expected: true,
		},
		{
			name:     "mem_enter <= mem_exit rejected",
			mutate:   func(t *ThresholdConfig) { t.MemEnter, t.MemExit = 0.78, 0.85 },
			expected: false,
		},
		{
			name:     "mem_hard <= mem_enter rejected",
			mutate:   func(t *ThresholdConfig) { t.MemEnter, t.MemHard = 0.92, 0.85 },
			expected: false,
		},
		{
			name:     "negative mem_exit rejected",
			mutate:   func(t *ThresholdConfig) { t.MemExit = -0.1 },
			expected: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := normalizeConfig(Config{Enabled: true})
			tc.mutate(&config.Thresholds)
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
	assert.Equal(t, defaultMemEnter, config.Thresholds.MemEnter)
	assert.Equal(t, defaultMemExit, config.Thresholds.MemExit)
	assert.Equal(t, defaultMemHard, config.Thresholds.MemHard)
	assert.Less(t, config.Thresholds.MemExit, config.Thresholds.MemEnter)
	assert.Less(t, config.Thresholds.MemEnter, config.Thresholds.MemHard)
}
