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
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

const (
	defaultSampleInterval = 250 * time.Millisecond
	defaultCPUSlowBeta    = 0.95
	defaultCPUFastBeta    = 0.7
	defaultCPUEnter       = 0.95
	defaultCPUExit        = 0.85
	defaultCPUHard        = 1.2
	defaultMemEnter       = 0.75
	defaultMemExit        = 0.7
	defaultMemHard        = 0.85
	defaultBreachN        = 3
	defaultMemBreachN     = 1
)

var throttleRecordTypes = []define.RecordType{
	define.RecordTraces,
	define.RecordMetrics,
	define.RecordLogs,
	define.RecordProfiles,
}

type Config struct {
	Enabled        bool                  `config:"enabled" mapstructure:"enabled"`                 // 总开关；关闭时 middleware 直接放行，不初始化采样回路。
	SampleInterval time.Duration         `config:"sample_interval" mapstructure:"sample_interval"` // 采样周期；缺省 250ms。
	Signal         SignalConfig          `config:"signal" mapstructure:"signal"`                   // CPU 信号采样参数。
	Thresholds     ThresholdConfig       `config:"thresholds" mapstructure:"thresholds"`           // 全局阈值，所有数据类型共用。
	Rules          map[string]RuleConfig `config:"rules" mapstructure:"rules"`                     // 按数据类型调丢弃强度，支持 default/traces/metrics/logs/profiles。
}

type SignalConfig struct {
	// 权重用于平滑信号，参考：https://datatracker.ietf.org/doc/html/rfc6298
	CPUSlowBeta   float64 `config:"cpu_slow_beta" mapstructure:"cpu_slow_beta"`   // 慢信号 EWMA 历史权重。
	CPUFastBeta   float64 `config:"cpu_fast_beta" mapstructure:"cpu_fast_beta"`   // 快信号 EWMA 历史权重。
	FallbackCores float64 `config:"fallback_cores" mapstructure:"fallback_cores"` // 读不到 CPU 配额时的有效核数，0 表示取 define.CoreNum()。
}

type ThresholdConfig struct {
	CPU ThresholdSlotConfig `config:"cpu" mapstructure:"cpu"` // CPU 信号阈值，slow=CPUSlow，fast=CPUFast。
	Mem ThresholdSlotConfig `config:"mem" mapstructure:"mem"` // 内存信号阈值，slow=fast=Mem。
}

type ThresholdSlotConfig struct {
	Enabled *bool   `config:"enabled" mapstructure:"enabled"`   // false 表示该信号不参与进入、退出、熔断和丢弃概率计算。
	Enter   float64 `config:"enter" mapstructure:"enter"`       // slow 信号进入线，连续 breach_n 次越线进入「降级」。
	Exit    float64 `config:"exit" mapstructure:"exit"`         // slow 信号退出线，连续 breach_n 次回落退出「降级」。
	Hard    float64 `config:"hard" mapstructure:"hard"`         // fast 信号熔断线，连续 breach_n 次越线进入「熔断」。
	BreachN int     `config:"breach_n" mapstructure:"breach_n"` // 连续命中门控，同时作用于 enter、exit、hard 和 hard clear。
}

type RuleConfig struct {
	Enabled *bool    `config:"enabled" mapstructure:"enabled"`   // false 表示该数据类型不做限流。
	DropMin *float64 `config:"drop_min" mapstructure:"drop_min"` // 丢弃概率下界。
	DropMax *float64 `config:"drop_max" mapstructure:"drop_max"` // 丢弃概率上界。
}

type Rule struct {
	Enabled bool
	DropMin float64
	DropMax float64
}

func normalizeConfig(c Config) Config {
	if c.SampleInterval <= 0 {
		c.SampleInterval = defaultSampleInterval
	}
	if c.Signal.CPUSlowBeta == 0 {
		c.Signal.CPUSlowBeta = defaultCPUSlowBeta
	}
	if c.Signal.CPUFastBeta == 0 {
		c.Signal.CPUFastBeta = defaultCPUFastBeta
	}
	c.Thresholds.CPU = normalizeThresholdSlot(c.Thresholds.CPU, defaultCPUEnter, defaultCPUExit, defaultCPUHard, defaultBreachN)
	c.Thresholds.Mem = normalizeThresholdSlot(c.Thresholds.Mem, defaultMemEnter, defaultMemExit, defaultMemHard, defaultMemBreachN)
	return c
}

func normalizeThresholdSlot(c ThresholdSlotConfig, enter, exit, hard float64, breachN int) ThresholdSlotConfig {
	if c.Enabled == nil {
		enabled := true
		c.Enabled = &enabled
	}
	if c.Enter == 0 {
		c.Enter = enter
	}
	if c.Exit == 0 {
		c.Exit = exit
	}
	if c.Hard == 0 {
		c.Hard = hard
	}
	if c.BreachN <= 0 {
		c.BreachN = breachN
	}
	return c
}

func validateConfig(c Config) error {
	if !c.Enabled {
		return nil
	}
	if c.SampleInterval <= 0 {
		return fmt.Errorf("sample_interval must be greater than 0")
	}
	if c.Signal.CPUSlowBeta < 0 || c.Signal.CPUSlowBeta >= 1 {
		return fmt.Errorf("signal.cpu_slow_beta must be in [0, 1)")
	}
	if c.Signal.CPUFastBeta < 0 || c.Signal.CPUFastBeta >= 1 {
		return fmt.Errorf("signal.cpu_fast_beta must be in [0, 1)")
	}
	if c.Signal.FallbackCores < 0 {
		return fmt.Errorf("signal.fallback_cores must be greater than or equal to 0")
	}
	if err := validateThresholdSlot("cpu", c.Thresholds.CPU); err != nil {
		return err
	}
	if err := validateThresholdSlot("mem", c.Thresholds.Mem); err != nil {
		return err
	}
	for name, rc := range c.Rules {
		if !validRuleName(name) {
			return fmt.Errorf("rules.%s is not supported", name)
		}
		if rc.DropMin != nil && (*rc.DropMin < 0 || *rc.DropMin > 1) {
			return fmt.Errorf("rules.%s.drop_min must be in [0, 1]", name)
		}
		if rc.DropMax != nil && (*rc.DropMax < 0 || *rc.DropMax > 1) {
			return fmt.Errorf("rules.%s.drop_max must be in [0, 1]", name)
		}
	}
	for recordType, rule := range buildRules(c.Rules) {
		if !rule.Enabled {
			continue
		}
		if rule.DropMin > rule.DropMax {
			return fmt.Errorf("rules.%s.drop_min must be less than or equal to drop_max after merging default rule", recordType.S())
		}
	}
	return nil
}

func validateThresholdSlot(name string, c ThresholdSlotConfig) error {
	if !thresholdEnabled(c) {
		return nil
	}
	if c.Exit < 0 {
		return fmt.Errorf("thresholds.%s.exit must be greater than or equal to 0", name)
	}
	if c.Enter <= c.Exit {
		return fmt.Errorf("thresholds.%s.enter must be greater than thresholds.%s.exit", name, name)
	}
	if c.Hard <= c.Enter {
		return fmt.Errorf("thresholds.%s.hard must be greater than thresholds.%s.enter", name, name)
	}
	if c.BreachN <= 0 {
		return fmt.Errorf("thresholds.%s.breach_n must be greater than 0", name)
	}
	return nil
}

func thresholdEnabled(c ThresholdSlotConfig) bool {
	return c.Enabled == nil || *c.Enabled
}

func validRuleName(name string) bool {
	switch name {
	case "default", define.RecordTraces.S(), define.RecordMetrics.S(), define.RecordLogs.S(), define.RecordProfiles.S():
		return true
	default:
		return false
	}
}

// buildRules 把每类数据的规则与 default 合并成最终 Rule，没单独配置的类型直接用 default。
func buildRules(configs map[string]RuleConfig) map[define.RecordType]Rule {
	defaultRule := Rule{Enabled: true, DropMin: 0, DropMax: 1}
	if rc, ok := configs["default"]; ok {
		defaultRule = mergeRule(defaultRule, rc)
	}

	rules := make(map[define.RecordType]Rule, len(throttleRecordTypes))
	for _, rt := range throttleRecordTypes {
		rule := defaultRule
		if rc, ok := configs[rt.S()]; ok {
			rule = mergeRule(rule, rc)
		}
		rules[rt] = rule
	}
	return rules
}

func mergeRule(base Rule, config RuleConfig) Rule {
	if config.Enabled != nil {
		base.Enabled = *config.Enabled
	}
	if config.DropMin != nil {
		base.DropMin = *config.DropMin
	}
	if config.DropMax != nil {
		base.DropMax = *config.DropMax
	}
	return base
}

func fallbackCores(c Config) float64 {
	if c.Signal.FallbackCores > 0 {
		return c.Signal.FallbackCores
	}
	return float64(define.CoreNum())
}
