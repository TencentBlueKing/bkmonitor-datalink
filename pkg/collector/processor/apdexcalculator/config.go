// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package apdexcalculator

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstrings"
)

type Config struct {
	Calculator CalculatorConfig `config:"calculator" mapstructure:"calculator"`
	Rules      []RuleConfig     `config:"rules" mapstructure:"rules"`

	rules         map[ruleKey]RuleConfig
	predicateKeys *mapstrings.MapStrings // key: kind
	defaultRule   *RuleConfig
}

type ruleKey struct {
	Kind         string
	PredicateKey string
}

func (c *Config) Setup() {
	rules := make(map[ruleKey]RuleConfig)
	predicateKeys := mapstrings.New(mapstrings.OrderDesc)
	for i := 0; i < len(c.Rules); i++ {
		rule := c.Rules[i]
		// 但如果 rule 中配置了 duration 且 start_event 和 end_event 都存在 则优先级高于不配置 duration 的规则 因此单独成一类存储
		if rule.Duration != nil {
			rules[ruleKey{Kind: rule.Kind, PredicateKey: rule.PredicateKey}] = rule
			predicateKeys.Set(rule.Kind, rule.PredicateKey)
			continue
		}
		// Note: 与 saas 约定如若没找到匹配的 Kind 类型 则统一使用空 Kind 的配置作为兜底
		// 仅当 kind="" 且 predicate_key="" 时才作为无条件兜底
		// 如果 kind="" 但 predicate_key 不为空，则当普通规则存储，支持按 predicate 匹配
		if rule.Kind == "" && rule.PredicateKey == "" {
			c.defaultRule = &rule
		}

		// 剩余情况确保 rule.Kind 是不为空的
		rules[ruleKey{Kind: rule.Kind, PredicateKey: rule.PredicateKey}] = rule
		predicateKeys.Set(rule.Kind, rule.PredicateKey)
	}
	c.rules = rules
	c.predicateKeys = predicateKeys
}

func (c *Config) GetPredicateKeys(kind string) []string {
	// 返回当前 kind 的 predicateKeys 和默认 kind "" 的 predicateKeys 的合并
	// 优先返回当前 kind 的 keys，然后再补充默认 kind 的 keys（去重）
	kindKeys := c.predicateKeys.Get(kind)
	defaultKeys := c.predicateKeys.Get("")

	if len(defaultKeys) == 0 {
		return kindKeys
	}
	if len(kindKeys) == 0 {
		return defaultKeys
	}

	// 合并两个列表，去重，优先保留当前 kind 的顺序
	seen := make(map[string]struct{}, len(kindKeys)+len(defaultKeys))
	result := make([]string, 0, len(kindKeys)+len(defaultKeys))

	for _, key := range kindKeys {
		seen[key] = struct{}{}
		result = append(result, key)
	}

	for _, key := range defaultKeys {
		if _, ok := seen[key]; !ok {
			result = append(result, key)
		}
	}

	return result
}

func (c *Config) Rule(kind, predicateKey string) (RuleConfig, bool) {
	// 优先查询精确匹配：(kind, predicateKey)
	if v, ok := c.rules[ruleKey{Kind: kind, PredicateKey: predicateKey}]; ok {
		return v, ok
	}
	// 如果 predicateKey 不为空，再尝试查询默认 kind 的该 predicateKey：("", predicateKey)
	if predicateKey != "" {
		if v, ok := c.rules[ruleKey{Kind: "", PredicateKey: predicateKey}]; ok {
			return v, ok
		}
	}
	// 最后回退到兜底规则
	if c.defaultRule != nil {
		return *c.defaultRule, true
	}

	return RuleConfig{}, false
}

type RuleConfig struct {
	Kind         string              `config:"kind" mapstructure:"kind"`
	MetricName   string              `config:"metric_name" mapstructure:"metric_name"`
	Destination  string              `config:"destination" mapstructure:"destination"`
	PredicateKey string              `config:"predicate_key" mapstructure:"predicate_key"`
	ApdexT       float64             `config:"apdex_t" mapstructure:"apdex_t"`
	Duration     *RuleDurationConfig `config:"duration" mapstructure:"duration"`
}

type RuleDurationConfig struct {
	StartEvent string `config:"start_event" mapstructure:"start_event"`
	EndEvent   string `config:"end_event" mapstructure:"end_event"`
}

type CalculatorConfig struct {
	Type        string `config:"type" mapstructure:"type"`
	ApdexStatus string `config:"apdex_status" mapstructure:"apdex_status"`
}
