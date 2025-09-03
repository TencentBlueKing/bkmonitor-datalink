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
	"github.com/spf13/cast"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"golang.org/x/exp/slices"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
)

const (
	ActionUpsert = "upsert"
)

type Op string

const (
	OpIn    Op = "in"
	OpNotIn Op = "notin"
	OpRange Op = "range"
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
	Metrics      []string             `config:"metrics" mapstructure:"metrics"`
	Rules        RelabelRules         `config:"rules" mapstructure:"rules"`
	Destinations []RelabelDestination `config:"destinations" mapstructure:"destinations"`
}

func (r *RelabelAction) IsMetricIn(name string) bool {
	return slices.Contains(r.Metrics, name)
}

func (r *RelabelAction) Validate() error {
	if len(r.Metrics) == 0 || len(r.Destinations) == 0 {
		return errors.New("relabel action: no metrics or destinations")
	}
	return r.Rules.Validate()
}

type RelabelDestination struct {
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

type RelabelRules []RelabelRule

func (rs *RelabelRules) Validate() error {
	for i := 0; i < len(*rs); i++ {
		if err := (*rs)[i].Validate(); err != nil {
			return err
		}
	}
	return nil
}

// MatchRWLabels 判断 RemoteWrite labels 是否匹配所有规则
func (rs *RelabelRules) MatchRWLabels(labels PromLabels) bool {
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
func (rs *RelabelRules) MatchMetricAttrs(attrs pcommon.Map) bool {
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

type CodeRelabelAction struct {
	Metrics  []string             `config:"metrics" mapstructure:"metrics"`
	Target   string               `config:"target" mapstructure:"target"`
	Services []RelabelDestination `config:"destinations" mapstructure:"destinations"`
}
