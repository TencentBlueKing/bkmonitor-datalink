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

func TestManagerStateHysteresis(t *testing.T) {
	manager := newManager(testConfig())
	manager.Publish(WaterLevel{CPUSlow: 0.81, CPUFast: 0.5})
	assert.Equal(t, StateNormal, manager.State(define.RecordTraces))

	manager.Publish(WaterLevel{CPUSlow: 0.82, CPUFast: 0.5})
	assert.Equal(t, StateShedding, manager.State(define.RecordTraces))
	assert.Equal(t, ActionShed, manager.decide(define.RecordTraces, func() float64 { return 0 }))

	manager.Publish(WaterLevel{CPUSlow: 0.75, CPUFast: 0.5})
	manager.Publish(WaterLevel{CPUSlow: 0.75, CPUFast: 0.5})
	assert.Equal(t, StateShedding, manager.State(define.RecordTraces))

	manager.Publish(WaterLevel{CPUSlow: 0.69, CPUFast: 0.5})
	assert.Equal(t, StateShedding, manager.State(define.RecordTraces))
	manager.Publish(WaterLevel{CPUSlow: 0.68, CPUFast: 0.5})
	assert.Equal(t, StateNormal, manager.State(define.RecordTraces))
}

func TestManagerHardOpen(t *testing.T) {
	manager := newManager(testConfig())
	manager.Publish(WaterLevel{CPUSlow: 0.5, CPUFast: 0.95})
	assert.Equal(t, StateNormal, manager.State(define.RecordTraces))

	manager.Publish(WaterLevel{CPUSlow: 0.5, CPUFast: 0.96})
	assert.Equal(t, StateOpen, manager.State(define.RecordTraces))
	assert.Equal(t, ActionOpen, manager.Decide(define.RecordTraces))

	manager.Publish(WaterLevel{CPUSlow: 0.75, CPUFast: 0.5})
	assert.Equal(t, StateShedding, manager.State(define.RecordTraces))
}

func TestManagerMemOpenAndRuleDisabled(t *testing.T) {
	enabled := false
	config := testConfig()
	config.Rules = map[string]RuleConfig{
		define.RecordMetrics.S(): {Enabled: &enabled},
	}
	manager := newManager(config)

	manager.Publish(WaterLevel{CPUSlow: 0.1, CPUFast: 0.1, Mem: 0.99, MemValid: true})
	assert.Equal(t, StateOpen, manager.State(define.RecordTraces))
	assert.Equal(t, StateNormal, manager.State(define.RecordMetrics))
	assert.Equal(t, ActionAdmit, manager.Decide(define.RecordMetrics))
}

func TestManagerDropProbability(t *testing.T) {
	dropMin := 0.2
	dropMax := 0.8
	config := testConfig()
	config.Rules = map[string]RuleConfig{
		"default": {
			DropMin: &dropMin,
			DropMax: &dropMax,
		},
	}
	manager := newManager(config)

	manager.Publish(WaterLevel{CPUSlow: 0.85, CPUFast: 0.5})
	assert.InDelta(t, 0.5, manager.dropProbability(define.RecordTraces), 0.001)

	manager.Publish(WaterLevel{CPUSlow: 0.95, CPUFast: 0.5})
	assert.InDelta(t, 0.8, manager.dropProbability(define.RecordTraces), 0.001)
}

func testConfig() Config {
	return normalizeConfig(Config{
		Enabled: true,
		Thresholds: ThresholdConfig{
			CPUEnter: 0.8,
			CPUExit:  0.7,
			CPUHard:  0.9,
			MemHard:  0.92,
			BreachN:  2,
		},
	})
}
