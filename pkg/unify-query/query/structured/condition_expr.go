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
	"regexp"
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

// TableIDConditionExpr 表ID条件表达式：仅支持多条件 AND（全部满足）
// 例如 scene=log,cluster_id=cls-1 => Conditions: [{scene,eq,log},{cluster_id,eq,cls-1}]
type TableIDConditionExpr struct {
	Conditions []LabelCondition
}

// Empty 是否为空（无任何条件）
func (e *TableIDConditionExpr) Empty() bool {
	if e == nil {
		return true
	}
	return len(e.Conditions) == 0
}

// matchLabelsExpr 根据表达式求值：labels 满足 expr 则返回 true（所有条件 AND）
// 每条条件按 op 求值（eq/neq/reg/nreg）
func matchLabelsExpr(labels map[string]string, expr *TableIDConditionExpr) bool {
	if expr == nil || expr.Empty() {
		return false
	}
	if len(labels) == 0 {
		return false
	}
	for _, c := range expr.Conditions {
		if !singleConditionMatches(labels, c) {
			return false
		}
	}
	return true
}

func singleConditionMatches(labels map[string]string, c LabelCondition) bool {
	actual, ok := labels[c.Key]
	switch c.Op {
	case LabelOpEq:
		return ok && actual == c.Value
	case LabelOpNeq:
		if !ok {
			return c.Value != "" // 不存在的 key，neq "" 为 true
		}
		return actual != c.Value
	case LabelOpReg:
		if !ok {
			return false
		}
		re, err := regexp.Compile(c.Value)
		if err != nil {
			return false
		}
		return re.MatchString(actual)
	case LabelOpNreg:
		if !ok {
			return true // 不存在的 key，nreg 视为匹配
		}
		re, err := regexp.Compile(c.Value)
		if err != nil {
			return true
		}
		return !re.MatchString(actual)
	default:
		return false
	}
}
