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
	"encoding/hex"
	"fmt"
	"slices"
)

// BuiltinMergeEventSourceID 保留给系统内部合并生产者，外部 Cleaner 不得占用。
const BuiltinMergeEventSourceID = "builtin_alarm_merge"

// MergeOrigin 标识已持久化合并裁决生成的内部 Event，不保存无限成员列表。
// 该标记只能由系统内部创建路径写入，不能从外部标准事件载荷透传。
type MergeOrigin struct {
	OperationID   string        `json:"operation_id"`
	Policy        PolicyVersion `json:"policy"`
	WindowID      string        `json:"window_id"`
	MembersDigest string        `json:"members_digest"`
	MemberCount   int           `json:"member_count"`
	// AlarmTags 是冻结策略显式追加的标签，按 ID 递增且不重复。
	AlarmTags []int64 `json:"alarm_tags,omitempty"`
}

// Clone 返回独立的内部来源标记。
func (o *MergeOrigin) Clone() *MergeOrigin {
	if o == nil {
		return nil
	}
	v := *o
	v.AlarmTags = slices.Clone(o.AlarmTags)
	if len(v.AlarmTags) == 0 {
		v.AlarmTags = nil
	}
	return &v
}

// Validate 限定内置来源与合并操作相互匹配；普通来源不能借此绕过再次合并。
func (o *MergeOrigin) Validate(source string) error {
	if o == nil {
		if source == BuiltinMergeEventSourceID {
			return fmt.Errorf("builtin merge source requires origin")
		}
		return nil
	}
	if source != BuiltinMergeEventSourceID || o.MemberCount < 2 || o.MemberCount > 256 {
		return fmt.Errorf("invalid merge origin scope")
	}
	if len(o.AlarmTags) > 128 {
		return fmt.Errorf("merge origin has too many tags")
	}
	for i, tag := range o.AlarmTags {
		if tag <= 0 || tag >= 1<<53 || (i > 0 && o.AlarmTags[i-1] >= tag) {
			return fmt.Errorf("invalid merge origin tags")
		}
	}
	if err := o.Policy.Validate(); err != nil {
		return err
	}
	for _, s := range []string{o.OperationID, o.WindowID, o.MembersDigest} {
		raw, err := hex.DecodeString(s)
		if err != nil || len(raw) != 32 {
			return fmt.Errorf("invalid merge origin identity")
		}
	}
	return nil
}
