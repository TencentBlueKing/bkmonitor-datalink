// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fta

import (
	"fmt"
	"regexp"

	"github.com/cstockton/go-conv"
	"github.com/jmespath/go-jmespath"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	RuleConditionOr = "or"

	RuleMethodEq    = "eq"
	RuleMethodNeq   = "neq"
	RuleMethodRegex = "reg"
)

// CleanConfig 清洗配置
type CleanConfig struct {
	// 支持多个清洗配置，可以通过rules配置来决定使用哪个配置
	CleanConfigs []*struct {
		Alerts         []*Alert         `mapstructure:"alert_config" json:"alert_config"`
		Normalizations []*Normalization `mapstructure:"normalization_config" json:"normalization_config"`
		Rules          []*Rule          `mapstructure:"rules" json:"rules"`

		exprMap map[string]*jmespath.JMESPath `mapstructure:"-"`
	} `mapstructure:"clean_configs" json:"clean_configs"`
	// 原本的清洗配置，为了兼容旧版，保留，同时也可以作为默认配置
	DefaultNormalizations []*Normalization `mapstructure:"normalization_config" json:"normalization_config"`
	DefaultAlerts         []*Alert         `mapstructure:"alert_config" json:"alert_config"`

	defaultExprMap map[string]*jmespath.JMESPath `mapstructure:"-"`
}

// NewCleanConfig 新建清洗配置
func NewCleanConfig(config interface{}) (*CleanConfig, error) {
	cleanConfig := &CleanConfig{}
	err := mapstructure.Decode(config, cleanConfig)
	if err != nil {
		return nil, err
	}

	// 初始化所有配置
	for _, c := range cleanConfig.CleanConfigs {
		c.exprMap, err = ConvertToJMESPath(c.Normalizations)
		if err != nil {
			return nil, err
		}
		for _, rule := range c.Rules {
			err = rule.Init()
			if err != nil {
				return nil, err
			}
		}
		for _, alert := range c.Alerts {
			err = alert.Init()
			if err != nil {
				return nil, err
			}
		}
	}
	cleanConfig.defaultExprMap, err = ConvertToJMESPath(cleanConfig.DefaultNormalizations)
	if err != nil {
		return nil, err
	}
	for _, alert := range cleanConfig.DefaultAlerts {
		err = alert.Init()
		if err != nil {
			return nil, err
		}
	}

	return cleanConfig, nil
}

// GetMatchConfig 获取匹配的配置
func (c *CleanConfig) GetMatchConfig(data interface{}) ([]*Alert, map[string]*jmespath.JMESPath, error) {
	// 遍历所有配置，如果匹配到，则返回
	for _, c := range c.CleanConfigs {
		if isRulesMatch(c.Rules, data) {
			return c.Alerts, c.exprMap, nil
		}
	}
	// 如果没有匹配到，则返回默认配置
	return c.DefaultAlerts, c.defaultExprMap, nil
}

// Normalization 字段提取配置
type Normalization struct {
	Field string `mapstructure:"field" json:"field"`
	Expr  string `mapstructure:"expr" json:"expr"`
}

// ConvertToJMESPath 转换为JMESPath
func ConvertToJMESPath(normalizations []*Normalization) (map[string]*jmespath.JMESPath, error) {
	exprMap := make(map[string]*jmespath.JMESPath)
	for _, normalization := range normalizations {
		// 如果表达式为空，则跳过
		if normalization.Expr == "" {
			continue
		}

		expr, err := utils.CompileJMESPathCustom(normalization.Expr)
		if err != nil {
			return nil, errors.WithMessagef(err, "expr compiled error for expr->(%s)", normalization.Expr)
		}
		exprMap[normalization.Field] = expr
	}
	return exprMap, nil
}

// Alert 告警名称匹配规则
type Alert struct {
	Name  string  `mapstructure:"name" json:"name"`
	Rules []*Rule `mapstructure:"rules" json:"rules"`
}

// Init 初始化
func (a *Alert) Init() error {
	for _, rule := range a.Rules {
		err := rule.Init()
		if err != nil {
			return err
		}
	}
	return nil
}

// IsMatch 判断当前数据是否满足匹配规则
func (a *Alert) IsMatch(data interface{}) bool {
	return isRulesMatch(a.Rules, data)
}

// GetMatchAlertName 匹配告警名称
func getMatchAlertName(alerts []*Alert, data interface{}) (string, error) {
	for _, alert := range alerts {
		if alert.IsMatch(data) {
			return alert.Name, nil
		}
	}
	return "", fmt.Errorf("no alert name matched")
}

// Rule 单条匹配规则
type Rule struct {
	Key       string   `json:"key" mapstructure:"key"`
	Value     []string `json:"value" mapstructure:"value"`
	Method    string   `json:"method" mapstructure:"method"`
	Condition string   `json:"condition" mapstructure:"condition"`
	searcher  *jmespath.JMESPath
	matcher   Matcher
}

// isRulesMatch 判断当前数据是否满足匹配规则
func isRulesMatch(rules []*Rule, data interface{}) bool {
	logging.Debugf("trigger match start, data: %+v", data)
	if len(rules) == 0 {
		// 如果条件为空，则必定为 true
		return true
	}

	isMatch := true
	for i, rule := range rules {
		// 如果是第一次匹配，则直接匹配
		// 如果是or，且目前状态为false，则继续匹配
		// 如果是and，且目前状态为true，则继续匹配
		if i == 0 || (isMatch && rule.Condition != RuleConditionOr) || (!isMatch && rule.Condition == RuleConditionOr) {
			isMatch, _ = rule.IsMatch(data)
			continue
		}
		// 如果有一个分组匹配完成，且结果为true，后续的or条件不再匹配
		if isMatch && rule.Condition == RuleConditionOr {
			return true
		}
		// 如果结果已经为false，则跳过后续的and条件
		if !isMatch && rule.Condition != RuleConditionOr {
			continue
		}
	}
	// 如果没有匹配到，则返回false
	return isMatch
}

func (r *Rule) Init() error {
	var err error
	if r.searcher == nil {
		r.searcher, err = utils.CompileJMESPathCustom(r.Key)
		if err != nil {
			return errors.WithMessagef(err, "rule compiled error for key->(%s)", r.Key)
		}
	}
	matcher, err := NewMatcher(r.Method, r.Value)
	if err != nil {
		return errors.WithMessagef(err, "matcher init failed for rule->(%+v)", r)
	}
	r.matcher = matcher
	return nil
}

func (r *Rule) IsMatch(actual interface{}) (bool, error) {
	search, err := r.searcher.Search(actual)
	if err != nil {
		return false, errors.WithMessagef(err, "search data error: %+v", actual)
	}
	return r.matcher.IsMatch(search)
}

type Matcher interface {
	SetExcepted(excepted []string) error
	IsMatch(actual interface{}) (bool, error)
}

func NewMatcher(method string, excepted []string) (Matcher, error) {
	var matcher Matcher
	switch method {
	case RuleMethodEq:
		matcher = new(EqualMatcher)
	case RuleMethodNeq:
		matcher = new(NotEqualMatcher)
	case RuleMethodRegex:
		matcher = new(RegexMatcher)
	default:
		return nil, fmt.Errorf("unsupported rule method type->(%s)", method)
	}
	err := matcher.SetExcepted(excepted)
	if err != nil {
		return nil, err
	}
	return matcher, nil
}

// BaseMatcher 匹配器的基类
type BaseMatcher struct {
	Excepted []string
}

func (m *BaseMatcher) SetExcepted(excepted []string) error {
	m.Excepted = excepted
	return nil
}

// EqualMatcher 相等条件匹配器
type EqualMatcher struct {
	BaseMatcher
}

func (m *EqualMatcher) IsMatch(actual interface{}) (bool, error) {
	actualString := conv.String(actual)
	for _, exceptedString := range m.Excepted {
		if actualString == exceptedString {
			return true, nil
		}
	}
	return false, nil
}

// NotEqualMatcher 不相等条件匹配器
type NotEqualMatcher struct {
	BaseMatcher
}

func (m *NotEqualMatcher) IsMatch(actual interface{}) (bool, error) {
	actualString := conv.String(actual)
	for _, exceptedString := range m.Excepted {
		if actualString == exceptedString {
			return false, nil
		}
	}
	return true, nil
}

// RegexMatcher 正则条件匹配器
type RegexMatcher struct {
	BaseMatcher
	regexps []*regexp.Regexp
}

func (m *RegexMatcher) IsMatch(actual interface{}) (bool, error) {
	actualString := []byte(conv.String(actual))
	for _, regex := range m.regexps {
		if regex.Match(actualString) {
			return true, nil
		}
	}
	return false, nil
}

func (m *RegexMatcher) SetExcepted(excepted []string) error {
	m.Excepted = excepted
	m.regexps = nil
	for _, regexString := range excepted {
		regex, err := regexp.Compile(regexString)
		if err != nil {
			return errors.WithMessagef(err, "regex compile error for string->(%s)", regexString)
		}
		m.regexps = append(m.regexps, regex)
	}
	return nil
}
