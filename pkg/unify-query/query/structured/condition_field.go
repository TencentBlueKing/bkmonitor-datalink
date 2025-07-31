// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/prometheus/prometheus/model/labels"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

const (
	ConditionEqual       = "eq"
	ConditionNotEqual    = "ne"
	ConditionRegEqual    = "req"
	ConditionNotRegEqual = "nreq"
	ConditionContains    = "contains"
	ConditionNotContains = "ncontains"

	ConditionExisted    = "existed"
	ConditionNotExisted = "nexisted"
)

const (
	ConditionExact = "exact"
	ConditionGt    = "gt"
	ConditionGte   = "gte"
	ConditionLt    = "lt"
	ConditionLte   = "lte"
)

const (
	SqlEqual    = "="
	SqlNotEqual = "!="
	SqlReg      = "REGEXP"
	SqlNotReg   = "NOT REGEXP"
	SqlGt       = ">"
	SqlGte      = ">="
	SqlLt       = "<"
	SqlLte      = "<="
)

// 特殊处理的字段
const (
	BizID     = "bk_biz_id"
	ProjectID = "project_id"
	ClusterID = "bcs_cluster_id"
)

func PromOperatorToConditions(matchType labels.MatchType) string {
	switch matchType {
	case labels.MatchEqual:
		return ConditionEqual
	case labels.MatchNotEqual:
		return ConditionNotEqual
	case labels.MatchRegexp:
		return ConditionRegEqual
	case labels.MatchNotRegexp:
		return ConditionNotRegEqual
	default:
		log.Errorf(context.TODO(), "failed to translate op->[%s] to condition op.Will return default op", matchType)
		return ConditionEqual
	}
}

// ConditionField 过滤条件的字段描述
type ConditionField struct {
	// DimensionName 过滤字段
	DimensionName string `json:"field_name" example:"bk_biz_id"`
	// Value 查询值
	Value []string `json:"value" example:"2"`
	// Operator 操作符，包含：eq,ne,erq,nreq,contains,ncontains
	Operator string `json:"op" example:"contains"`
	// IsWildcard 是否是通配符
	IsWildcard bool `json:"is_wildcard,omitempty"`
	// IsPrefix 是否是前缀
	IsPrefix bool `json:"is_prefix,omitempty"`
	// IsSuffix 是否是后缀
	IsSuffix bool `json:"is_suffix,omitempty"`
	// IsForceEq 是否强制等于
	IsForceEq bool `json:"is_force_eq,omitempty"`
}

// String
func (c *ConditionField) String() string {
	return fmt.Sprintf("dimension_name->[%s] op->[%s] value->[%s]", c.DimensionName, c.Operator, c.Value)
}

// ToPromOperator
func (c *ConditionField) ToPromOperator() labels.MatchType {
	switch c.Operator {
	case ConditionEqual:
		return labels.MatchEqual
	case ConditionNotEqual:
		return labels.MatchNotEqual
	case ConditionContains:
		// 包含的过滤条件，改为使用正则，然后多个条件用正则竖线分割提供
		return labels.MatchRegexp
	case ConditionNotContains:
		return labels.MatchNotRegexp
	case ConditionRegEqual:
		return labels.MatchRegexp
	case ConditionNotRegEqual:
		return labels.MatchNotRegexp
	default:
		return labels.MatchEqual
	}
}

func (c *ConditionField) BkSql() *ConditionField {
	if len(c.Value) == 0 {
		return nil
	}

	// bksql 查询遇到单引号需要转义
	for k, v := range c.Value {
		if strings.Contains(v, "'") {
			v = strings.ReplaceAll(v, "'", "''")
			c.Value[k] = v
		}
	}

	switch c.Operator {
	case ConditionEqual, ConditionExact, ConditionContains:
		c.Operator = SqlEqual
	case ConditionNotEqual, ConditionNotContains:
		c.Operator = SqlNotEqual
	case ConditionRegEqual:
		c.Operator = SqlReg
	case ConditionNotRegEqual:
		c.Operator = SqlNotReg
	case ConditionGt:
		c.Operator = SqlGt
	case ConditionGte:
		c.Operator = SqlGte
	case ConditionLt:
		c.Operator = SqlLt
	case ConditionLte:
		c.Operator = SqlLte
	}

	return c
}

// ContainsToPromReg 将结构化查询中的contains条件改为 正则 "x|y" 的方式
func (c *ConditionField) ContainsToPromReg() *ConditionField {
	// value 个数为 0, 不处理
	if len(c.Value) == 0 {
		return c
	}

	// value 个数为 1, 转换为 等于/不等于, 效率最高
	if len(c.Value) == 1 {
		switch c.Operator {
		case ConditionContains:
			c.Operator = ConditionEqual
		case ConditionNotContains:
			c.Operator = ConditionNotEqual
		}
		return c
	}

	isRegx := false
	// value 个数大于 1，转换为正则表达式处理
	switch c.Operator {
	case ConditionEqual:
		c.Operator = ConditionRegEqual
	case ConditionNotEqual:
		c.Operator = ConditionNotRegEqual
	case ConditionContains:
		c.Operator = ConditionRegEqual
	case ConditionNotContains:
		c.Operator = ConditionNotRegEqual
	case ConditionRegEqual:
		isRegx = true
	case ConditionNotRegEqual:
		isRegx = true
	}

	// 防止contains中含有特殊字符，导致错误的正则匹配，需要预先转义一下
	var resultValues = make([]string, 0, len(c.Value))
	for _, v := range c.Value {
		var nv string
		if isRegx {
			nv = v
		} else {
			nv = regexp.QuoteMeta(v)
		}
		resultValues = append(resultValues, nv)
	}

	newValue := strings.Join(resultValues, "|")

	// 如果非正则查询需要补充头尾，达到完全匹配
	if !isRegx {
		newValue = fmt.Sprintf("^(%s)$", newValue)
	}
	c.Value = []string{newValue}

	return c
}

// LabelMatcherToConditions
func LabelMatcherToConditions(lm []*labels.Matcher) (string, []ConditionField, error) {
	var (
		metricName string
		conds      = make([]ConditionField, 0, len(lm))
	)

	for _, label := range lm {
		if label.Name == labels.MetricName {
			metricName = label.Value
			continue
		}

		cond := ConditionField{
			DimensionName: label.Name,
			Value:         []string{label.Value},
		}

		op := convertOp(label.Type)
		if op == "" {
			return metricName, nil, fmt.Errorf("failed to decode the '%s' operation symbol", label.Type)
		}
		cond.Operator = op
		conds = append(conds, cond)
	}
	return metricName, conds, nil
}
