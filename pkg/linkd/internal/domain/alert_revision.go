// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain

import "fmt"

// ValidateAlertCreation 只校验创建阶段的状态、版本和初始同步水位；调用方须先规范化并完成字段校验。
func ValidateAlertCreation(a Alert) error {
	if a.Status != AlertStatusActive || a.Revision != 1 {
		return fmt.Errorf("new alert must be active at revision 1")
	}
	if err := validateActionTransition(nil, a); err != nil {
		return err
	}
	for _, target := range a.Projection.Targets {
		if target.SyncedRevision != 0 || target.SyncedAt != nil {
			return fmt.Errorf("new alert cannot start with confirmed projection")
		}
	}
	return nil
}

// PrepareAlertReplacement 为仓储 CAS 计算业务版本，再执行完整替换校验。
// 输入可以携带读取时的 revision，或计划已经冻结的下一版；其他值拒绝，不能任意重置版本。
// UpdateAt 只区分已定义的业务替换与元数据完成，不用于计算版本值；版本始终来自当前计数器加一。
func PrepareAlertReplacement(current, replacement Alert) (Alert, error) {
	if err := current.Validate(); err != nil {
		return Alert{}, err
	}
	next := replacement.Clone()
	revision := current.Revision
	if next.UpdateAt.After(current.UpdateAt) {
		if revision >= 1<<53-1 {
			return Alert{}, fmt.Errorf("alert revision exhausted")
		}
		revision++
	}
	if next.Revision != current.Revision && next.Revision != revision {
		return Alert{}, fmt.Errorf("replacement revision differs from current or next business version")
	}
	next.Revision = revision
	if revision > current.Revision {
		if err := advanceProjectionRequirements(current, &next); err != nil {
			return Alert{}, err
		}
	}
	next, err := next.Normalize()
	if err != nil {
		return Alert{}, err
	}
	if err := ValidateAlertReplacement(current, next); err != nil {
		return Alert{}, err
	}
	return next, nil
}
