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
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	RuleConditionAnd = "and"
	RuleConditionOr  = "or"
)

const (
	RuleMethodEq    = "eq"
	RuleMethodNeq   = "neq"
	RuleMethodRegex = "reg"
)

// Trigger: 告警匹配触发器
type Trigger struct {
	Rules      []*Rule `json:"rules"`
	ruleGroups [][]*Rule
}

func (t *Trigger) Init() error {
	var group []*Rule
	t.ruleGroups = nil
	for _, rule := range t.Rules {
		err := rule.Init()
		if err != nil {
			return errors.WithMessagef(err, "trigger init error for config->(%+v)", rule)
		}
		if rule.Condition == RuleConditionOr && len(group) > 0 {
			// 如果是OR条件，则对条件进行拆分
			t.ruleGroups = append(t.ruleGroups, group)
			// 重置切片
			group = nil
		}
		group = append(group, rule)
	}
	if len(group) > 0 {
		t.ruleGroups = append(t.ruleGroups, group)
	}
	return nil
}

// IsMatch: 判断当前数据是否满足匹配规则
func (t *Trigger) IsMatch(actual interface{}) bool {
	var isMatch bool

	logging.Debugf("trigger match start, data: %+v", actual)
	if len(t.ruleGroups) == 0 {
		// 如果条件为空，则必定为 true
		return true
	}

	for _, group := range t.ruleGroups {
		// 多个组之间是 or 的关系，任意一个组匹配规则就成立
		isMatch = true
		for _, rule := range group {
			// 单个组内的规则都是 and 的关系，必须要全部匹配规则才成立
			result, err := rule.IsMatch(actual)
			// 忽略 err，err 被认为匹配失败
			if err != nil || !result {
				isMatch = false
				logging.Debugf("trigger rule match result->(false), rule: %+v", rule)
				break
			}
			logging.Debugf("trigger rule match result->(true), rule: %+v", rule)
		}
		if isMatch {
			logging.Debugf("trigger rule group match result->(true), group: %+v", group)
			return true
		} else {
			logging.Debugf("trigger rule group match result->(false), group: %+v", group)
		}
	}
	return false
}

// Rule: 单条匹配规则
type Rule struct {
	Key       string   `json:"key"`
	Value     []string `json:"value"`
	Method    string   `json:"method"`
	Condition string   `json:"condition"`
	searcher  *jmespath.JMESPath
	matcher   Matcher
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
