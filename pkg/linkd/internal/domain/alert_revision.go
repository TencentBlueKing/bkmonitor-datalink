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
	"fmt"
	"reflect"
)

// ValidateAlertCreation 只校验创建阶段的状态、版本和初始同步水位；调用方须先规范化并完成字段校验。
func ValidateAlertCreation(a Alert) error {
	if a.Status != AlertStatusActive || a.Revision != 1 {
		return fmt.Errorf("new alert must be active at revision 1")
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
	return prepareAlertReplacement(current, replacement, false)
}

// PrepareAlertReplacementFromSnapshot 复用同一 Repository 已规范化且保持不可变的 current 载荷。
// replacement 的动态 JSON 或聚合状态不同仍完整校验；不能用于外部构造的 current。
func PrepareAlertReplacementFromSnapshot(current, replacement Alert) (Alert, error) {
	if err := current.ValidateState(); err != nil {
		return Alert{}, err
	}
	return prepareAlertReplacement(current, replacement, true)
}

func prepareAlertReplacement(current, replacement Alert, trusted bool) (Alert, error) {
	next := replacement.cloneState()
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
	next, err := next.normalizeState()
	if err != nil {
		return Alert{}, err
	}
	unchanged := trusted && next.EnrichStatus == current.EnrichStatus &&
		reflect.DeepEqual(next.ExtraData, current.ExtraData) && reflect.DeepEqual(next.Enrich, current.Enrich)
	if unchanged {
		// 绑定到可信快照的载荷，不能保留调用方另行构造的可变 map/RawMessage。
		next.ExtraData, next.Enrich = current.ExtraData, current.Enrich
	} else {
		next, err = next.normalizePayload()
		if err != nil {
			return Alert{}, err
		}
	}
	if err := validateAlertReplacementTransition(current, next); err != nil {
		return Alert{}, err
	}
	return next, nil
}

// SameAlertBusinessSnapshot 忽略已完成意图和复查时间，供已落库计划的幂等恢复判断。
// revision 仍属于比较范围，不能用新业务快照确认旧计划完成。
func SameAlertBusinessSnapshot(a, b Alert) bool {
	strip := func(v Alert) Alert {
		v.PolicyChange = nil
		v.MergeChange = nil
		v.Shield.NextCheckAt = nil
		return v
	}
	a, b = strip(a), strip(b)
	aExtra, bExtra, aEnrich, bEnrich := a.ExtraData, b.ExtraData, a.Enrich, b.Enrich
	a.ExtraData, b.ExtraData, a.Enrich, b.Enrich = nil, nil, nil, nil
	if !reflect.DeepEqual(a, b) {
		return false
	}
	// 计划保留来源 JSON，存储读取可能已规范化；仅在幂等恢复边界比较 JSON 语义。
	// 身份、revision 和其他业务字段仍须严格相同，不能用格式差异掩盖业务冲突。
	sameJSON := func(left, right JSONObject) bool {
		if reflect.DeepEqual(left, right) {
			return true
		}
		left, leftErr := left.Normalize()
		right, rightErr := right.Normalize()
		return leftErr == nil && rightErr == nil && reflect.DeepEqual(left, right)
	}
	return sameJSON(aExtra, bExtra) && sameJSON(aEnrich, bEnrich)
}
