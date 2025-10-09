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
		// Note: 与 saas 约定如若没找到匹配的 Kind 类型 则统一使用空 Kind 的配置作为兜底
		// 兜底配置都且只有一个
		if rule.Kind == "" {
			c.defaultRule = &rule
			continue
		}

		// 剩余情况确保 rule.Kind 是不为空的
		rules[ruleKey{Kind: rule.Kind, PredicateKey: rule.PredicateKey}] = rule
		predicateKeys.Set(rule.Kind, rule.PredicateKey)
	}
	c.rules = rules
	c.predicateKeys = predicateKeys
}

func (c *Config) GetPredicateKeys(kind string) []string {
	return c.predicateKeys.Get(kind)
}

func (c *Config) Rule(kind, predicateKey string) (RuleConfig, bool) {
	if v, ok := c.rules[ruleKey{Kind: kind, PredicateKey: predicateKey}]; ok {
		return v, ok
	}
	if c.defaultRule != nil {
		return *c.defaultRule, true
	}

	return RuleConfig{}, false
}

type RuleConfig struct {
	Kind         string  `config:"kind" mapstructure:"kind"`
	MetricName   string  `config:"metric_name" mapstructure:"metric_name"`
	Destination  string  `config:"destination" mapstructure:"destination"`
	PredicateKey string  `config:"predicate_key" mapstructure:"predicate_key"`
	ApdexT       float64 `config:"apdex_t" mapstructure:"apdex_t"`
}

type CalculatorConfig struct {
	Type        string `config:"type" mapstructure:"type"`
	ApdexStatus string `config:"apdex_status" mapstructure:"apdex_status"`
}
