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

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
)

type Config struct {
	Drop    DropAction      `config:"drop" mapstructure:"drop"`
	Replace []ReplaceAction `config:"replace" mapstructure:"replace"`
	Relabel []RelabelAction `config:"relabel" mapstructure:"relabel"`
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

// RelabelMetricIfMatched 如果 metric 命中所有规则，重定义相应属性
func (r *RelabelAction) RelabelMetricIfMatched(metric pmetric.Metric) {
	if r.Metric != metric.Name() {
		return
	}
	foreach.MetricsDataPointsAttrs(metric, func(attrs pcommon.Map) {
		if r.Rules.Match(attrs) {
			for _, destination := range r.Destinations {
				attrs.UpsertString(destination.Label, destination.Value)
			}
		}
	})
}

type Destination struct {
	Label string `config:"label" mapstructure:"label"`
	Value string `config:"value" mapstructure:"value"`
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
}

type Rules []Rule

// Match 判断属性是否匹配所有规则
func (rs *Rules) Match(attrs pcommon.Map) bool {
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

// Match 判断某个值是否命中本规则
func (r *Rule) Match(value string) bool {
	switch r.Op {
	case OperatorIn:
		for _, v := range r.Values {
			v := v.(string)
			if value == v {
				return true
			}
		}
	case OperatorNotIn:
		for _, v := range r.Values {
			if value == v {
				return false
			}
		}
		return true
	case OperatorRange:
		for _, v := range r.Values {
			v := v.(map[string]interface{})

			prefix, ok := v["prefix"]
			if ok {
				if !strings.HasPrefix(value, prefix.(string)) {
					continue
				}
				value = strings.TrimPrefix(value, prefix.(string))
			}

			value, err := strconv.Atoi(value)
			if err != nil {
				continue
			}
			minVal := v["min"]
			maxVal := v["max"]
			if minVal == nil || maxVal == nil {
				continue
			}
			if uint64(value) >= minVal.(uint64) && uint64(value) <= maxVal.(uint64) {
				return true
			}
		}
	}
	return false
}
