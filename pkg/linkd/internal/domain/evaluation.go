// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain

import (
	"cmp"
	"fmt"
)

// MaxEventEvaluations 是单次多级别裁决的硬上限。
const MaxEventEvaluations = 32

// EventEvaluation 表示某一标准级别在本次事件中的判定，同级别只能出现一次。
type EventEvaluation struct {
	// Severity 是映射后的标准级别名；来源 Draft 在工厂映射前暂存来源级别。
	Severity string `json:"severity"`
	// Action 是本级别的来源动作，不表达整条事件的汇总状态。
	Action EventAction `json:"action"`
	// ActionReason 是本级别动作的来源原因，最多 256 bytes。
	ActionReason string `json:"action_reason"`
}

// ValidateEvaluations 校验非空、有界且标准级别唯一的判定集合。
func ValidateEvaluations(evaluations []EventEvaluation) error {
	if len(evaluations) == 0 || len(evaluations) > MaxEventEvaluations {
		return fmt.Errorf("evaluations must contain between 1 and %d items", MaxEventEvaluations)
	}
	seen := make(map[string]bool, len(evaluations))
	for _, evaluation := range evaluations {
		if err := validateTextLength("severity", evaluation.Severity, 1, 32); err != nil {
			return err
		}
		if seen[evaluation.Severity] {
			return fmt.Errorf("evaluations contain duplicate severity")
		}
		seen[evaluation.Severity] = true
		if !evaluation.Action.Valid() {
			return fmt.Errorf("evaluation action is invalid")
		}
		if err := validateOptionalTextLength("action_reason", evaluation.ActionReason, 256); err != nil {
			return err
		}
	}
	return nil
}

func compareEvaluation(a, b EventEvaluation) int { return cmp.Compare(a.Severity, b.Severity) }
