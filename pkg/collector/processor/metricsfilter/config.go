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
	"golang.org/x/exp/slices"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
)

type Config struct {
	Drop    DropAction      `config:"drop" mapstructure:"drop"`
	Replace []ReplaceAction `config:"replace" mapstructure:"replace"`
	Relabel []RelabelAction `config:"relabel" mapstructure:"relabel"`
}

func (c *Config) Validate() error {
	for i := 0; i < len(c.Relabel); i++ {
		if err := c.Relabel[i].Validate(); err != nil {
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
	Metrics      []string      `config:"metrics" mapstructure:"metrics"`
	Rules        Rules         `config:"rules" mapstructure:"rules"`
	Destinations []Destination `config:"destinations" mapstructure:"destinations"`
}

func (r *RelabelAction) IsMetricIn(name string) bool {
	return slices.Contains(r.Metrics, name)
}

func (r *RelabelAction) Validate() error {
	if len(r.Metrics) == 0 || len(r.Destinations) == 0 {
		return errors.New("relabel action: no metrics/destinations")
	}
	return r.Rules.Validate()
}

type DstAction string

const (
	ActionUpsert DstAction = "upsert"
)

type Operator string

const (
	OperatorIn    Operator = "in"
	OperatorNotIn Operator = "notin"
	OperatorRange Operator = "range"
)

type Destination struct {
	Action DstAction `config:"action" mapstructure:"action"`
	Label  string    `config:"label" mapstructure:"label"`
	Value  string    `config:"value" mapstructure:"value"`
}

type RangeValue struct {
	Prefix string `config:"prefix" mapstructure:"prefix"`
	Min    int    `config:"min" mapstructure:"min"`
	Max    int    `config:"max" mapstructure:"max"`
}

type Rule struct {
	Label  string        `config:"label" mapstructure:"label"`
	Op     Operator      `config:"op" mapstructure:"op"`
	Values []interface{} `config:"values" mapstructure:"values"`

	inValues    []string
	rangeValues []RangeValue
}

// Match 判断某个值是否命中本规则
func (r *Rule) Match(value string) bool {
	switch r.Op {
	case OperatorIn:
		return slices.Contains(r.inValues, value)

	case OperatorNotIn:
		return !slices.Contains(r.inValues, value)

	case OperatorRange:
		for _, v := range r.rangeValues {
			if v.Prefix != "" {
				if !strings.HasPrefix(value, v.Prefix) {
					continue
				}
				value = strings.TrimPrefix(value, v.Prefix)
			}
			i, err := strconv.Atoi(value)
			if err != nil {
				// 非数字值属于未命中规则，跳过
				continue
			}
			if i >= v.Min && i <= v.Max {
				return true
			}
		}
		return false
	}

	return false
}

// Validate 验证规则是否合法，并转换为对应的值
func (r *Rule) Validate() error {
	switch r.Op {
	case OperatorIn, OperatorNotIn:
		values := make([]string, 0, len(r.Values))
		for _, val := range r.Values {
			v, ok := val.(string)
			if !ok {
				return errors.Errorf("expected type string, but got %T", val)
			}
			values = append(values, v)
		}
		r.inValues = values

	case OperatorRange:
		values := make([]RangeValue, 0, len(r.Values))
		for _, val := range r.Values {
			v, ok := val.(map[string]interface{})
			if !ok {
				return errors.Errorf("expected type map, but got %T", val)
			}
			var rv RangeValue
			err := mapstructure.Decode(v, &rv)
			if err != nil {
				return errors.Wrapf(err, "failed to decode range value: %v", v)
			}
			if rv.Min > rv.Max {
				return errors.Errorf("invalid range value: %v", rv)
			}
			values = append(values, rv)
		}
		r.rangeValues = values

	default:
		return errors.Errorf("unsupported operator %s", r.Op)
	}
	return nil
}

// Rules 规则列表
// 使用指针方便 Validate 中修改内容
type Rules []Rule

func (rs *Rules) Validate() error {
	for i := 0; i < len(*rs); i++ {
		// 校验的同时需要修改，所以不使用 range 遍历
		if err := (*rs)[i].Validate(); err != nil {
			return err
		}
	}
	return nil
}

// MatchRWLabels 判断 RemoteWrite labels 是否匹配所有规则
func (rs *Rules) MatchRWLabels(labels PromLabels) bool {
	if len(*rs) == 0 {
		return false
	}
	for _, rule := range *rs {
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

// MatchMetricAttrs 判断 OT Metrics 属性是否匹配所有规则
func (rs *Rules) MatchMetricAttrs(attrs pcommon.Map) bool {
	if len(*rs) == 0 {
		return false
	}
	for _, rule := range *rs {
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

type PromLabels []prompb.Label

func (ls *PromLabels) Get(name string) (prompb.Label, bool) {
	if ls == nil {
		return prompb.Label{}, false
	}
	for i := 0; i < len(*ls); i++ {
		if (*ls)[i].Name == name {
			return (*ls)[i], true
		}
	}
	return prompb.Label{}, false
}

func (ls *PromLabels) Upsert(name, value string) {
	if ls == nil {
		return
	}
	for i := 0; i < len(*ls); i++ {
		if (*ls)[i].Name == name {
			(*ls)[i].Value = value
			return
		}
	}
	*ls = append(*ls, prompb.Label{Name: name, Value: value})
}
