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
	CPUEnter float64 `config:"cpu_enter" mapstructure:"cpu_enter"` // CPU 慢信号进入线，连续 breach_n 次越线进入「降级」。
	CPUExit  float64 `config:"cpu_exit" mapstructure:"cpu_exit"`   // CPU 慢信号退出线，低于该线可退出「降级」「熔断」。
	CPUHard  float64 `config:"cpu_hard" mapstructure:"cpu_hard"`   // CPU 快信号熔断线，连续 breach_n 次越线进入「熔断」。
	MemEnter float64 `config:"mem_enter" mapstructure:"mem_enter"` // 内存进入线，连续 breach_n 次越线进入「降级」。
	MemExit  float64 `config:"mem_exit" mapstructure:"mem_exit"`   // 内存退出线，低于该线可退出「降级」「熔断」。
	MemHard  float64 `config:"mem_hard" mapstructure:"mem_hard"`   // 内存熔断线，单次越线即熔断。
	BreachN  int     `config:"breach_n" mapstructure:"breach_n"`   // 连续越界次数门控。
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
	if c.Thresholds.CPUEnter == 0 {
		c.Thresholds.CPUEnter = defaultCPUEnter
	}
	if c.Thresholds.CPUExit == 0 {
		c.Thresholds.CPUExit = defaultCPUExit
	}
	if c.Thresholds.CPUHard == 0 {
		c.Thresholds.CPUHard = defaultCPUHard
	}
	if c.Thresholds.MemEnter == 0 {
		c.Thresholds.MemEnter = defaultMemEnter
	}
	if c.Thresholds.MemExit == 0 {
		c.Thresholds.MemExit = defaultMemExit
	}
	if c.Thresholds.MemHard == 0 {
		c.Thresholds.MemHard = defaultMemHard
	}
	if c.Thresholds.BreachN <= 0 {
		c.Thresholds.BreachN = defaultBreachN
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
	if c.Thresholds.CPUExit < 0 {
		return fmt.Errorf("thresholds.cpu_exit must be greater than or equal to 0")
	}
	if c.Thresholds.CPUEnter <= c.Thresholds.CPUExit {
		return fmt.Errorf("thresholds.cpu_enter must be greater than thresholds.cpu_exit")
	}
	if c.Thresholds.CPUHard <= c.Thresholds.CPUEnter {
		return fmt.Errorf("thresholds.cpu_hard must be greater than thresholds.cpu_enter")
	}
	if c.Thresholds.MemExit < 0 {
		return fmt.Errorf("thresholds.mem_exit must be greater than or equal to 0")
	}
	if c.Thresholds.MemEnter <= c.Thresholds.MemExit {
		return fmt.Errorf("thresholds.mem_enter must be greater than thresholds.mem_exit")
	}
	if c.Thresholds.MemHard <= c.Thresholds.MemEnter {
		return fmt.Errorf("thresholds.mem_hard must be greater than thresholds.mem_enter")
	}
	if c.Thresholds.BreachN <= 0 {
		return fmt.Errorf("thresholds.breach_n must be greater than 0")
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
