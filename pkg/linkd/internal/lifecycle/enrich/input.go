// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich

import (
	"math"

	"linkd/internal/domain"
)

const (
	labelStrategyID        = "bk_strategy_id"
	labelStrategyHistoryID = "bk_strategy_history_id"
	labelBizID             = "bk_biz_id"
)

// RequiredIDs 是内置监控丰富入口校验后的稳定查询身份。
type RequiredIDs struct {
	StrategyID int64
	HistoryID  int64
	BizID      int64
}

// ValidateRequiredIDs 在外部查询前校验 BASE_COLLECT 必需的三个正整数标签。
func ValidateRequiredIDs(alert domain.Alert) (RequiredIDs, []Diagnostic) {
	fields := []struct {
		name string
		dest *int64
	}{
		{name: labelStrategyID},
		{name: labelStrategyHistoryID},
		{name: labelBizID},
	}
	ids := RequiredIDs{}
	fields[0].dest, fields[1].dest, fields[2].dest = &ids.StrategyID, &ids.HistoryID, &ids.BizID
	missing := make([]string, 0, len(fields))
	invalid := make([]string, 0, len(fields))
	for _, field := range fields {
		value, exists := alert.Labels[field.name]
		if !exists {
			missing = append(missing, "labels."+field.name)
			continue
		}
		number, ok := value.NumberValue()
		if !ok || number <= 0 || math.Trunc(number) != number || number > math.MaxInt64 {
			invalid = append(invalid, "labels."+field.name)
			continue
		}
		*field.dest = int64(number)
	}
	diagnostics := make([]Diagnostic, 0, 2)
	if len(missing) != 0 {
		diagnostics = append(diagnostics, Diagnostic{Code: DiagnosticCodeMissingField, Fields: missing})
	}
	if len(invalid) != 0 {
		diagnostics = append(diagnostics, Diagnostic{Code: DiagnosticCodeInvalidField, Fields: invalid})
	}
	return ids, diagnostics
}
