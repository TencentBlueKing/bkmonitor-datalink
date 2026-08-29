// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cstockton/go-conv"
	"github.com/jmespath/go-jmespath"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
)

const (
	RuleConditionOr = "or"

	RuleMethodEq      = "eq"
	RuleMethodNeq     = "neq"
	RuleMethodRegex   = "reg"
	RuleMethodNReg    = "nreg"
	RuleMethodInclude = "include"
	RuleMethodExclude = "exclude"
)

// MatchRule 单条匹配规则
type MatchRule struct {
	Key       string   `json:"key" mapstructure:"key"`
	Value     []string `json:"value" mapstructure:"value"`
	Method    string   `json:"method" mapstructure:"method"`
	Condition string   `json:"condition" mapstructure:"condition"`
	searcher  *jmespath.JMESPath
	matcher   Matcher
}

// IsRulesMatch 判断当前数据是否满足匹配规则
func IsRulesMatch(rules []*MatchRule, data interface{}) bool {
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

func (r *MatchRule) Init() error {
	var err error
	if r.searcher == nil {
		r.searcher, err = CompileJMESPathCustom(r.Key)
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

func (r *MatchRule) IsMatch(actual interface{}) (bool, error) {
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
	case RuleMethodNReg:
		matcher = &RegexMatcher{negative: true}
	case RuleMethodInclude:
		matcher = &ContainMatcher{}
	case RuleMethodExclude:
		matcher = &ContainMatcher{negative: true}
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
	regexps  []*regexp.Regexp
	negative bool // 是否为取反
}

func (m *RegexMatcher) IsMatch(actual interface{}) (bool, error) {
	actualString := []byte(conv.String(actual))
	for _, regex := range m.regexps {
		matched := regex.Match(actualString)
		if m.negative {
			matched = !matched
		}
		if matched {
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

type ContainMatcher struct {
	BaseMatcher
	negative bool // 是否为取反
}

func (m *ContainMatcher) IsMatch(actual interface{}) (bool, error) {
	actualString := conv.String(actual)
	for _, exceptedString := range m.Excepted {
		matched := strings.Contains(actualString, exceptedString)
		if m.negative {
			matched = !matched
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

func (m *ContainMatcher) SetExcepted(excepted []string) error {
	m.Excepted = excepted
	return nil
}
