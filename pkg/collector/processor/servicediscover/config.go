// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package servicediscover

import (
	"net/url"
	"regexp"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	MatchTypeAuto   = "auto"
	MatchTypeManual = "manual"
	MatchTypeRegex  = "regex"
)

type Config struct {
	Rules []*Rule `config:"rules" mapstructure:"rules"`
}

func (c *Config) Setup() {
	for _, rule := range c.Rules {
		if rule.MatchConfig.Regex != "" {
			re, err := regexp.Compile(rule.MatchConfig.Regex)
			if err != nil {
				logger.Errorf("failed to compile regex %v: %v", rule.MatchConfig.Regex, err)
				continue
			}
			rule.re = re
		}

		if rule.MatchType == MatchTypeAuto {
			mappings := make(map[string]string)
			for _, group := range rule.MatchGroups {
				mappings[group.Source] = group.Destination
			}
			rule.mappings = mappings
		}
	}

	// 优先级
	sort.Slice(c.Rules, func(i, j int) bool {
		return c.Rules[i].Type > c.Rules[j].Type
	})
}

type Rule struct {
	Type         string       `config:"type" mapstructure:"type"`
	Kind         string       `config:"kind" mapstructure:"kind"`
	Service      string       `config:"service" mapstructure:"service"`
	MatchType    string       `config:"match_type" mapstructure:"match_type"`
	MatchKey     string       `config:"match_key" mapstructure:"match_key"`
	PredicateKey string       `config:"predicate_key" mapstructure:"predicate_key"`
	MatchConfig  MatchConfig  `config:"rule" mapstructure:"rule"`
	ReplaceType  string       `config:"replace_type" mapstructure:"replace_type"`
	MatchGroups  []MatchGroup `config:"match_groups" mapstructure:"match_groups"`

	re       *regexp.Regexp
	mappings map[string]string
}

type MatchConfig struct {
	Regex  string      `config:"regex" mapstructure:"regex"`
	Host   RuleHost    `config:"host" mapstructure:"host"`
	Path   RulePath    `config:"path" mapstructure:"path"`
	Params []RuleParam `config:"params" mapstructure:"params"`
}

type MatchGroup struct {
	Source      string `config:"source" mapstructure:"source"`
	Destination string `config:"destination" mapstructure:"destination"`
	ConstVal    string `config:"const_val" mapstructure:"const_val"`
}

type RuleParam struct {
	Name     string `config:"name" mapstructure:"name"`
	Operator string `config:"operator" mapstructure:"operator"`
	Value    string `config:"value" mapstructure:"value"`
}

type RuleHost struct {
	Operator string `config:"operator" mapstructure:"operator"`
	Value    string `config:"value" mapstructure:"value"`
}

type RulePath struct {
	Operator string `config:"operator" mapstructure:"operator"`
	Value    string `config:"value" mapstructure:"value"`
}

func (r *Rule) AttributeValue() string {
	df, key := processor.DecodeDimensionFrom(r.MatchKey)
	if df == processor.DimensionFromAttribute {
		return key
	}
	return ""
}

func (r *Rule) ResourceValue() string {
	df, key := processor.DecodeDimensionFrom(r.MatchKey)
	if df == processor.DimensionFromResource {
		return key
	}
	return ""
}

func (r *Rule) MethodValue() string {
	df, key := processor.DecodeDimensionFrom(r.MatchKey)
	if df == processor.DimensionFromMethod {
		return key
	}
	return ""
}

func (r *Rule) Match(val string) (map[string]string, bool) {
	switch r.MatchType {
	case MatchTypeManual:
		mappings, matched := r.ManualMatched(val)
		return mappings, matched
	case MatchTypeRegex:
		mappings, matched := r.RegexMatched(val)
		return mappings, matched
	default:
		mappings, matched := r.AutoMatched(val)
		return mappings, matched
	}
}

func (r *Rule) ManualMatched(val string) (map[string]string, bool) {
	u, err := url.Parse(val)
	if err != nil {
		logger.Warnf("failed to parse url %v, error: %v", val, err)
		return nil, false
	}
	logger.Debugf("parsed url host=%+v, path=%+v, query=%+v", u.Host, u.Path, u.Query())

	if r.MatchConfig.Host.Value != "" {
		if !OperatorMatch(u.Host, r.MatchConfig.Host.Value, r.MatchConfig.Host.Operator) {
			return nil, false
		}
	}

	if r.MatchConfig.Path.Value != "" {
		if !OperatorMatch(u.Path, r.MatchConfig.Path.Value, r.MatchConfig.Path.Operator) {
			return nil, false
		}
	}

	for _, param := range r.MatchConfig.Params {
		val := u.Query().Get(param.Name)
		if val == "" {
			return nil, false
		}
		if !OperatorMatch(val, param.Value, param.Operator) {
			return nil, false
		}
	}

	m := make(map[string]string)
	for _, group := range r.MatchGroups {
		switch group.Source {
		case "path":
			m[group.Destination] = u.Path
		case "service":
			m[group.Destination] = r.Service
		}
	}
	return m, true
}

func (r *Rule) AutoMatched(val string) (map[string]string, bool) {
	u, err := url.Parse(val)
	if err != nil {
		return nil, false
	}

	if r.re == nil {
		return nil, false
	}

	match := r.re.FindStringSubmatch(u.String())
	groups := make(map[string]string)
	for i, name := range r.re.SubexpNames() {
		if i != 0 && name != "" && len(match) > i {
			groups[name] = match[i]
		}
	}
	if len(groups) == 0 {
		return nil, false
	}

	m := make(map[string]string)
	for k, v := range groups {
		if mappingKey, ok := r.mappings[k]; ok {
			m[mappingKey] = v
		}
	}
	return m, true
}

func (r *Rule) RegexMatched(val string) (map[string]string, bool) {
	if r.re == nil {
		return nil, false
	}

	match := r.re.FindStringSubmatch(val)
	if match == nil {
		return nil, false
	}

	regexGroups := make(map[string]string)
	for i, name := range r.re.SubexpNames() {
		if i != 0 && name != "" && len(match) > i {
			regexGroups[name] = match[i]
		}
	}
	m := make(map[string]string)
	for _, group := range r.MatchGroups {
		if group.ConstVal != "" {
			m[group.Destination] = group.ConstVal
			continue
		}
		if val, ok := regexGroups[group.Source]; ok {
			m[group.Destination] = val
		}
	}
	return m, true
}

type ConfigHandler struct {
	rules map[string][]*Rule
}

func NewConfigHandler(c *Config) *ConfigHandler {
	rules := make(map[string][]*Rule)
	for i := 0; i < len(c.Rules); i++ {
		r := c.Rules[i]
		rules[r.Kind] = append(rules[r.Kind], r)
	}

	return &ConfigHandler{rules: rules}
}

func (ch *ConfigHandler) Get(kind string) []*Rule {
	return ch.rules[kind]
}
