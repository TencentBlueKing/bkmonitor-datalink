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
	"github.com/prometheus/prometheus/prompb"
	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
)

type Config struct {
	Drop    DropAction      `config:"drop" mapstructure:"drop"`
	Replace []ReplaceAction `config:"replace" mapstructure:"replace"`
	Relabel []RelabelAction `config:"relabel" mapstructure:"relabel"`
}

func (c *Config) Validate() error {
	for _, relabel := range c.Relabel {
		if err := relabel.Validate(); err != nil {
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

type Operator string

const (
	OperatorIn    Operator = "in"
	OperatorNotIn Operator = "not in"
	OperatorRange Operator = "range"
)

type RelabelAction struct {
	Metric       string        `config:"metric" mapstructure:"metric"`
	Rules        Rules         `config:"rules" mapstructure:"rules"`
	Destinations []Destination `config:"destinations" mapstructure:"destinations"`
}

func (r *RelabelAction) Validate() error {
	if r.Metric == "" {
		return errors.Errorf("relabel action have no metric name")
	}
	if err := r.Rules.Validate(); err != nil {
		return err
	}
	if len(r.Destinations) == 0 {
		return errors.Errorf("relabel action have no destination: %v", r)
	}
	for _, d := range r.Destinations {
		if d.Label == "" || d.Value == "" {
			return errors.Errorf("relabel action have invalid destination: %v", r)
		}
	}
	return nil
}

type Destination struct {
	Label string `config:"label" mapstructure:"label"`
	Value string `config:"value" mapstructure:"value"`
}

type RangeValue struct {
	Prefix string `config:"prefix" mapstructure:"prefix"`
	Min    *int   `config:"min" mapstructure:"min"`
	Max    *int   `config:"max" mapstructure:"max"`
}

// Rules 规则列表
// 使用指针方便 Validate 中修改内容
type Rules []*Rule

type Rule struct {
	Label  string        `config:"label" mapstructure:"label"`
	Op     Operator      `config:"op" mapstructure:"op"`
	Values []interface{} `config:"values" mapstructure:"values"`

	InValues    []string
	RangeValues []RangeValue
}

func (rs *Rules) Validate() error {
	// todo 没有rules是否报错
	for _, rule := range *rs {
		if err := rule.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// MatchMetricAttrs 判断 ot metric 属性是否匹配所有规则
func (rs *Rules) MatchMetricAttrs(attrs pcommon.Map) bool {
	for _, rule := range *rs {
		value, exist := attrs.Get(rule.Label)
		if !exist {
			return false
		}
		if matched := rule.Match(value.AsString()); !matched {
			return false
		}
	}
	return true
}

// MatchRWLabels 判断 remote write data labels 是否匹配所有规则
func (rs *Rules) MatchRWLabels(labels map[string]*prompb.Label) bool {
	for _, rule := range *rs {
		label, ok := labels[rule.Label]
		if !ok {
			return false
		}
		// 某条规则未命中直接返回
		if matched := rule.Match(label.GetValue()); !matched {
			return false
		}
	}
	return true
}

// Match 判断某个值是否命中本规则
func (r *Rule) Match(value string) bool {
	switch r.Op {
	case OperatorIn:
		for _, v := range r.InValues {
			if value == v {
				return true
			}
		}
	case OperatorNotIn:
		for _, v := range r.InValues {
			if value == v {
				return false
			}
		}
		return true
	case OperatorRange:
		for _, v := range r.RangeValues {
			if v.Prefix != "" {
				if !strings.HasPrefix(value, v.Prefix) {
					continue
				}
				value = strings.TrimPrefix(value, v.Prefix)
			}
			value, err := strconv.Atoi(value)
			if err != nil {
				// 非数字值属于未命中规则，跳过
				continue
			}
			if value >= *v.Min && value <= *v.Max {
				return true
			}
		}
	}
	return false
}

// Validate 验证规则是否合法，并转换为对应的值
func (r *Rule) Validate() error {
	switch r.Op {
	case OperatorIn, OperatorNotIn:
		values := make([]string, 0, len(r.Values))
		for _, val := range r.Values {
			val, ok := val.(string)
			if !ok {
				return errors.Errorf("invalid in rule: %v", r)
			}
			values = append(values, val)
		}
		r.InValues = values
	case OperatorRange:
		values := make([]RangeValue, 0, len(r.Values))
		for _, val := range r.Values {
			val, ok := val.(map[string]interface{})
			if !ok {
				return errors.Errorf("invalid range rule: %v", r)
			}
			var rv RangeValue
			err := mapstructure.Decode(val, &rv)
			if err != nil {
				return errors.Wrapf(err, "failed to decode range value: %v", r.Values)
			}

			if rv.Min == nil || rv.Max == nil || *rv.Min > *rv.Max {
				return errors.Errorf("invalid range value: %v", r.Values)
			}
			values = append(values, rv)
		}
		r.RangeValues = values
	default:
		return errors.Errorf("unsupported operator %s, only support [in, not in, range] now!", r.Op)
	}
	return nil
}
