// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metricsfilter

import (
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"github.com/spf13/cast"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"golang.org/x/exp/slices"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/promlabels"
)

const (
	relabelUpsert = "upsert"
)

type Op string

const (
	OpIn    Op = "in"
	OpNotIn Op = "notin"
	OpRange Op = "range"

	opSkip Op = "*" // 内部逻辑 表示匹配所有内容
)

type Config struct {
	Drop        DropAction          `config:"drop" mapstructure:"drop"`
	Replace     []ReplaceAction     `config:"replace" mapstructure:"replace"`
	Relabel     []RelabelAction     `config:"relabel" mapstructure:"relabel"`
	CodeRelabel []CodeRelabelAction `config:"code_relabel" mapstructure:"code_relabel"`
}

func (c *Config) Validate() error {
	for i := 0; i < len(c.Relabel); i++ {
		if err := c.Relabel[i].Validate(); err != nil {
			return err
		}
	}

	for i := 0; i < len(c.CodeRelabel); i++ {
		if err := c.CodeRelabel[i].Validate(); err != nil {
			return err
		}
	}
	return nil
}

type DropAction struct {
	Metrics []string `config:"metrics" mapstructure:"metrics"`
}

type ReplaceAction struct {
	Source      string `config:"source" mapstructure:"source"`
	Destination string `config:"destination" mapstructure:"destination"`
}

type RelabelAction struct {
	Metrics []string       `config:"metrics" mapstructure:"metrics"`
	Rules   []*RelabelRule `config:"rules" mapstructure:"rules"`
	Target  RelabelTarget  `config:"target" mapstructure:"target"`

	rrs *RelabelRules
}

func (r *RelabelAction) IsMetricIn(name string) bool {
	return slices.Contains(r.Metrics, name)
}

func (r *RelabelAction) Validate() error {
	if len(r.Metrics) == 0 {
		return errors.New("relabel action: no metrics specified")
	}

	r.rrs = &RelabelRules{Rules: r.Rules}
	return r.rrs.Validate()
}

func (r *RelabelAction) MatchRWLabels(labels promlabels.Labels) bool {
	return r.rrs.MatchRWLabels(labels)
}

func (r *RelabelAction) MatchOTAttrs(attrs pcommon.Map) bool {
	return r.rrs.MatchOTAttrs(attrs)
}

type RelabelTarget struct {
	Action string `config:"action" mapstructure:"action"`
	Label  string `config:"label" mapstructure:"label"`
	Value  string `config:"value" mapstructure:"value"`
}

type RelabelRangeValue struct {
	Prefix string `config:"prefix" mapstructure:"prefix"`
	Min    int    `config:"min" mapstructure:"min"`
	Max    int    `config:"max" mapstructure:"max"`
}

type RelabelRule struct {
	Label  string `config:"label" mapstructure:"label"`
	Op     Op     `config:"op" mapstructure:"op"`
	Values []any  `config:"values" mapstructure:"values"`

	inValues    []string
	rangeValues []RelabelRangeValue
}

// Match 判断某个值是否命中本规则
func (r *RelabelRule) Match(value string) bool {
	switch r.Op {
	case OpIn:
		return slices.Contains(r.inValues, value)

	case OpNotIn:
		return !slices.Contains(r.inValues, value)

	case OpRange:
		for _, v := range r.rangeValues {
			if v.Prefix != "" {
				if !strings.HasPrefix(value, v.Prefix) {
					continue
				}
				value = strings.TrimPrefix(value, v.Prefix)
			}
			i, err := strconv.Atoi(value)
			if err != nil {
				continue // 非数字值属于未命中规则，跳过
			}
			if i >= v.Min && i <= v.Max {
				return true
			}
		}

	case opSkip:
		return true
	}
	return false
}

// Validate 验证规则是否合法，并转换为对应的值
func (r *RelabelRule) Validate() error {
	switch r.Op {
	case OpIn, OpNotIn:
		values := make([]string, 0, len(r.Values))
		for _, val := range r.Values {
			values = append(values, cast.ToString(val))
		}
		r.inValues = values

	case OpRange:
		values := make([]RelabelRangeValue, 0, len(r.Values))
		for _, val := range r.Values {
			var rv RelabelRangeValue
			if err := mapstructure.Decode(val, &rv); err != nil {
				return errors.Wrapf(err, "failed to decode range value: %v", val)
			}
			values = append(values, rv)
		}
		r.rangeValues = values

	case opSkip:
	default:
		return errors.Errorf("unsupported operator %s", r.Op)
	}
	return nil
}

type RelabelRules struct {
	Rules []*RelabelRule
	Any   bool
}

func (rs *RelabelRules) Validate() error {
	for i := 0; i < len(rs.Rules); i++ {
		if err := rs.Rules[i].Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (rs *RelabelRules) MatchRWLabels(labels promlabels.Labels) bool {
	if len(rs.Rules) == 0 {
		return false
	}

	// 只匹配一种规则
	if rs.Any {
		for _, rule := range rs.Rules {
			label, ok := labels.Get(rule.Label)
			if ok && rule.Match(label.GetValue()) {
				return true
			}
		}
		return false
	}

	// 匹配所有规则
	for _, rule := range rs.Rules {
		label, ok := labels.Get(rule.Label)
		if !ok {
			return false
		}
		if !rule.Match(label.GetValue()) {
			return false
		}
	}
	return true
}

func (rs *RelabelRules) MatchOTAttrs(attrs pcommon.Map) bool {
	if len(rs.Rules) == 0 {
		return false
	}

	// 只匹配一种规则
	if rs.Any {
		for _, rule := range rs.Rules {
			label, ok := attrs.Get(rule.Label)
			if ok && rule.Match(label.AsString()) {
				return true
			}
		}
		return false
	}

	// 匹配所有规则
	for _, rule := range rs.Rules {
		label, ok := attrs.Get(rule.Label)
		if !ok {
			return false
		}
		if !rule.Match(label.AsString()) {
			return false
		}
	}
	return true
}

const (
	labelServiceName   = "service_name"
	labelCalleeServer  = "callee_server"
	labelCalleeService = "callee_service"
	labelCalleeMethod  = "callee_method"
	labelCode          = "code"
)

type CodeRelabelAction struct {
	Metrics  []string              `config:"metrics" mapstructure:"metrics"`
	Source   string                `config:"source" mapstructure:"source"`
	Services []*CodeRelabelService `config:"services" mapstructure:"services"`

	rrs *RelabelRules
}

func (c *CodeRelabelAction) IsMetricIn(name string) bool {
	return slices.Contains(c.Metrics, name)
}

func (c *CodeRelabelAction) Validate() error {
	if len(c.Metrics) == 0 || len(c.Services) == 0 {
		return errors.New("relabel action: no metrics or services")
	}

	for i := 0; i < len(c.Services); i++ {
		if err := c.Services[i].Validate(); err != nil {
			return err
		}
	}

	c.rrs = &RelabelRules{
		Rules: []*RelabelRule{{
			Label:  labelServiceName,
			Op:     OpIn,
			Values: []any{c.Source},
		}},
	}
	return c.rrs.Validate()
}

func (c *CodeRelabelAction) MatchRWLabels(labels promlabels.Labels) bool {
	return c.rrs.MatchRWLabels(labels)
}

func (c *CodeRelabelAction) MatchOTAttrs(attrs pcommon.Map) bool {
	return c.rrs.MatchOTAttrs(attrs)
}

type CodeRelabelService struct {
	Name  string             `config:"name" mapstructure:"name"`
	Codes []*CodeRelabelCode `config:"codes" mapstructure:"codes"`

	rrs *RelabelRules
}

func (c *CodeRelabelService) Validate() error {
	for i := 0; i < len(c.Codes); i++ {
		if err := c.Codes[i].Validate(); err != nil {
			return err
		}
	}

	toOpVal := func(s string) (Op, []any) {
		if s == string(opSkip) {
			return opSkip, nil
		}
		return OpIn, []any{s}
	}

	parts := strings.Split(c.Name, ";")
	if len(parts) != 3 {
		return errors.New("source must be in format (server;service;method)")
	}

	var rrs []*RelabelRule
	for i, part := range parts {
		var label string
		switch i {
		case 0:
			label = labelCalleeServer
		case 1:
			label = labelCalleeService
		case 2:
			label = labelCalleeMethod
		}

		op, values := toOpVal(part)
		rrs = append(rrs, &RelabelRule{
			Label:  label,
			Op:     op,
			Values: values,
		})
	}

	c.rrs = &RelabelRules{Rules: rrs}
	return c.rrs.Validate()
}

func (c *CodeRelabelService) MatchRWLabels(labels promlabels.Labels) bool {
	return c.rrs.MatchRWLabels(labels)
}

func (c *CodeRelabelService) MatchOTAttrs(attrs pcommon.Map) bool {
	return c.rrs.MatchOTAttrs(attrs)
}

type CodeRelabelCode struct {
	Rule   string        `config:"rule" mapstructure:"rule"`
	Target RelabelTarget `config:"target" mapstructure:"target"`

	rrs *RelabelRules
}

func (c *CodeRelabelCode) Validate() error {
	toRangeVal := func(s string) (*RelabelRangeValue, error) {
		lst := strings.Split(s, "_")
		if len(lst) >= 3 {
			return nil, errors.New("rangeValue must be in format ([prefix_]code.min[~code.max])")
		}

		var prefix string
		if len(lst) == 2 {
			prefix = lst[0] + "_"
		}

		last := lst[len(lst)-1]
		var minVal, maxVal int
		var err error
		isRange := strings.Contains(last, "~")
		if isRange {
			for i, v := range strings.Split(last, "~") {
				if i == 0 {
					minVal, err = strconv.Atoi(v)
					if err != nil {
						return nil, err
					}
				} else {
					maxVal, err = strconv.Atoi(v)
					if err != nil {
						return nil, err
					}
				}
			}
		} else {
			val, err := strconv.Atoi(last)
			if err != nil {
				return nil, err
			}
			minVal = val
			maxVal = val
		}

		return &RelabelRangeValue{
			Prefix: prefix,
			Min:    minVal,
			Max:    maxVal,
		}, nil
	}

	var rrs []*RelabelRule
	for _, part := range strings.Split(c.Rule, ",") {
		rv, err := toRangeVal(part)
		if err != nil {
			return err
		}
		rrs = append(rrs, &RelabelRule{
			Label:  labelCode,
			Op:     OpRange,
			Values: []any{rv},
		})
	}

	c.rrs = &RelabelRules{Rules: rrs, Any: true}
	return c.rrs.Validate()
}

func (c *CodeRelabelCode) MatchRWLabels(labels promlabels.Labels) bool {
	return c.rrs.MatchRWLabels(labels)
}

func (c *CodeRelabelCode) MatchOTAttrs(attrs pcommon.Map) bool {
	return c.rrs.MatchOTAttrs(attrs)
}
