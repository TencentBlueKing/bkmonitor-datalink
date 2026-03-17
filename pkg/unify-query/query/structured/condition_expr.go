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

// oneConditionFieldSelectorString 将 ConditionField 格式化为 __query_label_selector 的一个条件片段；正则时值用双引号包裹。
func oneConditionFieldSelectorString(f ConditionField) string {
	op := f.Operator
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
	val := ""
	if len(f.Value) > 0 {
		val = f.Value[0]
	}
	if op == ConditionRegEqual || op == ConditionNotRegEqual {
		val = `"` + strings.ReplaceAll(val, `"`, `\"`) + `"`
	}
	return f.DimensionName + promOp + val
}

// AllConditionsToQueryLabelSelectorString 将 AllConditions 序列化为 __query_label_selector 的值，用于 TS→PromQL。
// 格式: scene=log,cluster_id=1 or scene=k8s；组内 AND 用逗号，组间 OR 用 " or "。
func AllConditionsToQueryLabelSelectorString(all AllConditions) string {
	if len(all) == 0 {
		return ""
	}
	var orParts []string
	for _, group := range all {
		var andParts []string
		for _, f := range group {
			andParts = append(andParts, oneConditionFieldSelectorString(f))
		}
		if len(andParts) > 0 {
			orParts = append(orParts, strings.Join(andParts, ","))
		}
	}
	return strings.Join(orParts, " or ")
}

// matchLabelsForAllConditions 表标签过滤：all 为空时不过滤（返回 true）；否则用 AllConditions.MatchLabels。
func matchLabelsForAllConditions(labels map[string]string, all AllConditions) bool {
	if len(all) == 0 {
		return true
	}
	ok, _ := all.MatchLabels(labels)
	return ok
}

// MapToTableIDConditions 将 map[string]string 转为单组 AND 的 AllConditions（仅 eq），用于测试或简单 eq 场景。
func MapToTableIDConditions(m map[string]string) AllConditions {
	if len(m) == 0 {
		return nil
	}
	group := make([]ConditionField, 0, len(m))
	for k, v := range m {
		group = append(group, ConditionField{
			DimensionName: k,
			Value:         []string{v},
			Operator:      ConditionEqual,
		})
	}
	return AllConditions{group}
}
