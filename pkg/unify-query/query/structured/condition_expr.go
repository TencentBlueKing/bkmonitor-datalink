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
	"strings"
)

// QueryLabelSelectorLabelName PromQL 中用于表标签路由的指标标签名，仅用于 DataList 路由，不下发存储
const QueryLabelSelectorLabelName = "__query_label_selector"

// LabelCondition 表标签单条条件，用于 TableIDConditionExpr
type LabelCondition struct {
	Key   string // 标签名
	Op    string // eq / ne / req / nreq
	Value string // 标签值
}

// TableIDConditionExpr 表标签条件表达式：OrGroups 外层为 OR，内层 []LabelCondition 为 AND
type TableIDConditionExpr struct {
	OrGroups [][]LabelCondition
}

// ToConditions 将 OrGroups 转为 Conditions，供 MatchLabels 使用
func (e *TableIDConditionExpr) ToConditions() *Conditions {
	if e == nil || len(e.OrGroups) == 0 {
		return &Conditions{}
	}
	var fieldList []ConditionField
	var conditionList []string
	for groupIdx, group := range e.OrGroups {
		for _, lc := range group {
			op := lc.Op
			if op == "" {
				op = ConditionEqual
			}
			fieldList = append(fieldList, ConditionField{
				DimensionName: lc.Key,
				Value:         []string{lc.Value},
				Operator:      op,
			})
		}
		if groupIdx < len(e.OrGroups)-1 {
			if len(group) > 1 {
				for i := 0; i < len(group)-1; i++ {
					conditionList = append(conditionList, ConditionAnd)
				}
			}
			conditionList = append(conditionList, ConditionOr)
		} else if len(group) > 1 {
			for i := 0; i < len(group)-1; i++ {
				conditionList = append(conditionList, ConditionAnd)
			}
		}
	}
	return &Conditions{
		FieldList:     fieldList,
		ConditionList: conditionList,
	}
}

// ToQueryLabelSelectorString 序列化为 __query_label_selector 的值，用于 TS→PromQL
// 格式: scene=log,cluster_id=1 or scene=k8s
func (e *TableIDConditionExpr) ToQueryLabelSelectorString() string {
	if e == nil || len(e.OrGroups) == 0 {
		return ""
	}
	var orParts []string
	for _, group := range e.OrGroups {
		var andParts []string
		for _, lc := range group {
			andParts = append(andParts, oneLabelConditionString(lc))
		}
		orParts = append(orParts, strings.Join(andParts, ","))
	}
	return strings.Join(orParts, " or ")
}

func oneLabelConditionString(lc LabelCondition) string {
	op := lc.Op
	if op == "" {
		op = ConditionEqual
	}
	var promOp string
	switch op {
	case ConditionEqual:
		promOp = "="
	case ConditionNotEqual:
		promOp = "!="
	case ConditionRegEqual:
		promOp = "=~"
	case ConditionNotRegEqual:
		promOp = "!~"
	default:
		promOp = "="
	}
	return lc.Key + promOp + lc.Value
}

// ConditionsToTableIDConditionExpr 将 Conditions 转为 TableIDConditionExpr（OrGroups）
func ConditionsToTableIDConditionExpr(c *Conditions) (*TableIDConditionExpr, error) {
	if c == nil || len(c.FieldList) == 0 {
		return nil, nil
	}
	all, err := c.AnalysisConditions()
	if err != nil || len(all) == 0 {
		return nil, err
	}
	orGroups := make([][]LabelCondition, 0, len(all))
	for _, row := range all {
		group := make([]LabelCondition, 0, len(row))
		for _, f := range row {
			val := ""
			if len(f.Value) > 0 {
				val = f.Value[0]
			}
			op := f.Operator
			if op == "" {
				op = ConditionEqual
			}
			group = append(group, LabelCondition{Key: f.DimensionName, Op: op, Value: val})
		}
		if len(group) > 0 {
			orGroups = append(orGroups, group)
		}
	}
	if len(orGroups) == 0 {
		return nil, nil
	}
	return &TableIDConditionExpr{OrGroups: orGroups}, nil
}

// matchLabelsExpr 表标签过滤：expr 为空或 OrGroups 为空时不过滤（返回 true）；否则用 expr.ToConditions().MatchLabels(labels)。
func matchLabelsExpr(labels map[string]string, expr *TableIDConditionExpr) bool {
	if expr == nil || len(expr.OrGroups) == 0 {
		return true
	}
	c := expr.ToConditions()
	ok, _ := c.MatchLabels(labels)
	return ok
}

// MapToTableIDConditionExpr 将 map[string]string 转为单组 AND 的 TableIDConditionExpr（仅 eq），用于测试或简单 eq 场景。
func MapToTableIDConditionExpr(m map[string]string) *TableIDConditionExpr {
	if len(m) == 0 {
		return nil
	}
	group := make([]LabelCondition, 0, len(m))
	for k, v := range m {
		group = append(group, LabelCondition{Key: k, Op: ConditionEqual, Value: v})
	}
	return &TableIDConditionExpr{OrGroups: [][]LabelCondition{group}}
}
