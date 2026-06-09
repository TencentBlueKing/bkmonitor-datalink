// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import "strings"

// normalizeCommaConditionValues 将结构化条件里的 eq/ne 单值逗号串规范化成多值数组。
// 该逻辑只处理 conditions，不改变 query_string 的字面量检索语义。
func normalizeCommaConditionValues(conditions AllConditions) AllConditions {
	if len(conditions) == 0 {
		return conditions
	}

	isCommaValueOperator := func(operator string) bool {
		return operator == ConditionEqual || operator == ConditionNotEqual
	}

	var normalized AllConditions
	for i, group := range conditions {
		for j, condition := range group {
			if !isCommaValueOperator(condition.Operator) {
				continue
			}
			values, ok := splitSingleCommaValue(condition.Value)
			if !ok {
				continue
			}
			if normalized == nil {
				normalized = cloneAllConditions(conditions)
			}
			normalized[i][j].Value = values
		}
	}

	if normalized == nil {
		return conditions
	}
	return normalized
}

// normalizeCommaConditions 将 Conditions 中的 eq/ne 单值逗号串规范化成多值数组。
func normalizeCommaConditions(conditions Conditions) Conditions {
	normalized := cloneConditions(conditions)
	for i, condition := range normalized.FieldList {
		if condition.Operator != ConditionEqual && condition.Operator != ConditionNotEqual {
			continue
		}
		values, ok := splitSingleCommaValue(condition.Value)
		if !ok {
			continue
		}
		normalized.FieldList[i].Value = values
	}
	return normalized
}

// splitSingleCommaValue 只负责把形如 []string{"a,b,c"} 的值拆成 []string{"a", "b", "c"}。
// operator 是否允许拆分由调用方判断；如果拆分后不足两个有效值，则保持原值不变。
func splitSingleCommaValue(values []string) ([]string, bool) {
	if len(values) != 1 || !strings.Contains(values[0], ",") {
		return values, false
	}

	parts := strings.Split(values[0], ",")
	splitValues := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		splitValues = append(splitValues, value)
	}

	if len(splitValues) <= 1 {
		return values, false
	}
	return splitValues, true
}

func cloneAllConditions(conditions AllConditions) AllConditions {
	if conditions == nil {
		return nil
	}

	clone := make(AllConditions, len(conditions))
	for i, group := range conditions {
		clone[i] = make([]ConditionField, len(group))
		for j, condition := range group {
			clone[i][j] = condition
			if condition.Value != nil {
				clone[i][j].Value = append([]string{}, condition.Value...)
			}
		}
	}
	return clone
}

func cloneConditions(conditions Conditions) Conditions {
	clone := Conditions{}
	if conditions.FieldList != nil {
		clone.FieldList = make([]ConditionField, len(conditions.FieldList))
		for i, condition := range conditions.FieldList {
			clone.FieldList[i] = condition
			if condition.Value != nil {
				clone.FieldList[i].Value = append([]string{}, condition.Value...)
			}
		}
	}
	if conditions.ConditionList != nil {
		clone.ConditionList = append([]string{}, conditions.ConditionList...)
	}
	return clone
}
