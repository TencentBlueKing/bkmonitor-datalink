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
	"github.com/prometheus/prometheus/model/labels"
)

const (
	LabelOpEq   = "eq"
	LabelOpNeq  = "neq"
	LabelOpReg  = "reg"
	LabelOpNreg = "nreg"
)

// LabelCondition 单条标签条件：key op value
type LabelCondition struct {
	Key   string
	Op    string // eq / neq / reg / nreg
	Value string
}

// TableIDConditionExpr 表ID条件表达式：支持 AND/OR。OrGroups 中每组为 AND，组间为 OR。
// 例如 scene=log,cluster_id=cls-1 or scene=k8s => OrGroups: [[{scene,eq,log},{cluster_id,eq,cls-1}], [{scene,eq,k8s}]]
type TableIDConditionExpr struct {
	OrGroups [][]LabelCondition
}

// Empty 是否为空（无任何条件）
func (e *TableIDConditionExpr) Empty() bool {
	if e == nil || len(e.OrGroups) == 0 {
		return true
	}
	for _, g := range e.OrGroups {
		if len(g) > 0 {
			return false
		}
	}
	return true
}

// ToConditions 转为 Conditions，便于复用 condition.go 的 MatchLabels 求值逻辑。组内 AND，组间 OR。无法识别的 op 会跳过，不参与匹配。
func (e *TableIDConditionExpr) ToConditions() Conditions {
	if e == nil || len(e.OrGroups) == 0 {
		return Conditions{}
	}
	var fieldList []ConditionField
	var conditionList []string
	prevGroupIdx := -1
	for gi, group := range e.OrGroups {
		for _, lc := range group {
			op := labelOpToConditionOp(lc.Op)
			if op == "" {
				continue
			}
			if len(fieldList) > 0 {
				if gi == prevGroupIdx {
					conditionList = append(conditionList, ConditionAnd)
				} else {
					conditionList = append(conditionList, ConditionOr)
				}
			}
			fieldList = append(fieldList, ConditionField{
				DimensionName: lc.Key,
				Value:         []string{lc.Value},
				Operator:      op,
			})
			prevGroupIdx = gi
		}
	}
	return Conditions{FieldList: fieldList, ConditionList: conditionList}
}

func labelOpToConditionOp(op string) string {
	switch op {
	case LabelOpEq:
		return ConditionEqual
	case LabelOpNeq:
		return ConditionNotEqual
	case LabelOpReg:
		return ConditionRegEqual
	case LabelOpNreg:
		return ConditionNotRegEqual
	default:
		return ""
	}
}

// ConditionsToTableIDConditionExpr 将结构化 Conditions（field_list + op）转为 TableIDConditionExpr，支持 AND/OR。
// 使用 Conditions.AnalysisConditions() 做校验与分组，所有 OR 组均保留；每条 ConditionField 经 ContainsToPromReg 后按 ToPromOperator 映射为 eq/neq/reg/nreg。
func ConditionsToTableIDConditionExpr(c Conditions) *TableIDConditionExpr {
	allGroups, err := c.AnalysisConditions()
	if err != nil || len(allGroups) == 0 {
		return nil
	}
	orGroups := make([][]LabelCondition, 0, len(allGroups))
	for _, group := range allGroups {
		conds := make([]LabelCondition, 0, len(group))
		for i := range group {
			f := group[i]
			if len(f.Value) == 0 && f.Operator != ConditionExisted && f.Operator != ConditionNotExisted {
				continue
			}
			norm := *(f.ContainsToPromReg())
			op := matchTypeToLabelOp(norm.ToPromOperator())
			if op == "" {
				continue
			}
			val := ""
			if len(norm.Value) > 0 {
				val = norm.Value[0]
			}
			conds = append(conds, LabelCondition{Key: norm.DimensionName, Op: op, Value: val})
		}
		if len(conds) > 0 {
			orGroups = append(orGroups, conds)
		}
	}
	if len(orGroups) == 0 {
		return nil
	}
	return &TableIDConditionExpr{OrGroups: orGroups}
}

// matchTypeToLabelOp 将 Prometheus labels.MatchType 映射为表标签条件的 op（eq/neq/reg/nreg）。
func matchTypeToLabelOp(mt labels.MatchType) string {
	switch mt {
	case labels.MatchEqual:
		return LabelOpEq
	case labels.MatchNotEqual:
		return LabelOpNeq
	case labels.MatchRegexp:
		return LabelOpReg
	case labels.MatchNotRegexp:
		return LabelOpNreg
	default:
		return ""
	}
}

// matchLabelsExpr 根据表达式求值：labels 满足 expr 则返回 true，复用 condition.go 的 MatchLabels 逻辑。
func matchLabelsExpr(labels map[string]string, expr *TableIDConditionExpr) bool {
	if expr == nil || expr.Empty() {
		return false
	}
	return expr.ToConditions().MatchLabels(labels)
}
