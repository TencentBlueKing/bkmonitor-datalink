// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package suppressioncleanup

import (
	"strings"

	"linkd/internal/domain"
	"linkd/internal/policy"
)

// MaxRecordBytes 覆盖两类各最多 512 个窗口及最坏 JSON 转义，超限不得静默截断明细。
const MaxRecordBytes = 2 << 20

// Window 是 Lua 删除时原子捕获的身份；owner 由所属 Record.Cause.AlertID 确定。
type Window struct {
	// ID 为运行态窗口身份，防抖包含 subject 和 counter，聚合只含窗口哈希。
	ID string `json:"id"`
	// Epoch 是被删代次，不从清理后的查询或当前时间推算。
	Epoch string `json:"epoch,omitempty"`
	// Missing 表示防抖元信息当时已丢失，仅删除残余计数/反向引用，代次未知。
	Missing bool `json:"missing,omitempty"`
}

// Validate 检查类型对应身份和已知/未知代次，未知不能伪装为空代次的正常窗口。
func (w Window) Validate(kind string) error {
	parts := strings.Split(w.ID, ":")
	if (kind == "clip" && len(parts) != 2) || (kind == "aggregation" && len(parts) != 1) || (kind != "clip" && kind != "aggregation") {
		return policy.ErrInvalid
	}
	for _, part := range parts {
		if !hashID(part) {
			return policy.ErrInvalid
		}
	}
	if w.Missing {
		if kind != "clip" || w.Epoch != "" {
			return policy.ErrInvalid
		}
	} else if w.Epoch == "" || len(w.Epoch) > domain.EntityIDMaxBytes {
		return policy.ErrInvalid
	}
	return nil
}

// ValidWindowFilter 不允许脱离方式/窗口的代次筛选。
func ValidWindowFilter(kind, id, epoch string) bool {
	if kind != "" && kind != "clip" && kind != "aggregation" {
		return false
	}
	if id != "" && (Window{ID: id, Epoch: "filter"}).Validate(kind) != nil {
		return false
	}
	return len(epoch) <= domain.EntityIDMaxBytes && (epoch == "" || id != "")
}

// WindowMatches 只匹配本次已确认删除的明细；零删除或结果未确认不冒充逐窗口记录。
func WindowMatches(r Record, kind, id, epoch string) bool {
	if kind == "" {
		return true
	}
	if r.Result == nil {
		return false
	}
	o := r.Result.Clip
	if kind == "aggregation" {
		o = r.Result.Aggregation
	}
	for _, w := range o.Windows {
		if (id == "" || w.ID == id) && (epoch == "" || w.Epoch == epoch) {
			return true
		}
	}
	return false
}
