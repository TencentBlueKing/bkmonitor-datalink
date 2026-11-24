// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package merge

import (
	"slices"
	"time"

	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
)

// DecisionFromWindow 只接受已冻结窗口，并固定全部待清理成员与成功选中成员，二者不能混为一谈。
func DecisionFromWindow(w redisstate.MergeWindow, release policy.Release) (Decision, error) {
	if w.Frozen == nil || release.TenantID != w.TenantID || release.Kind != policy.Merge || release.ID != w.Policy.ID || release.Version != w.Policy.Version || release.Compiled.Digest != w.Policy.Digest {
		return Decision{}, policy.ErrInvalid
	}
	wait := make([]string, 0, len(w.Members))
	committed := map[string]bool{}
	for _, m := range w.Members {
		wait = append(wait, m.Main.AlertID)
		committed[m.Main.AlertID] = m.Committed
	}
	slices.Sort(wait)
	if len(slices.Compact(slices.Clone(wait))) != len(wait) {
		return Decision{}, policy.ErrInvalid
	}
	for _, id := range w.Frozen.MemberIDs {
		if !committed[id] {
			return Decision{}, policy.ErrInvalid
		}
	}
	phase, reason := "capturing", ""
	if w.Frozen.Outcome == "failed" {
		phase = "releasing"
		reason = "conditions_not_met"
	}
	at := time.UnixMilli(w.Frozen.AtMillis).UTC()
	d := Decision{ID: w.Frozen.OperationID, TenantID: w.TenantID, WindowID: w.ID, GroupKey: w.GroupKey, Policy: release, FrozenAt: at, StartedAt: time.UnixMilli(w.StartedAtMillis).UTC(), Deadline: time.UnixMilli(w.DeadlineMillis).UTC(), Outcome: w.Frozen.Outcome, MemberIDs: slices.Clone(w.Frozen.MemberIDs), WaitMemberIDs: wait, Progress: Progress{Phase: phase, ReasonCode: reason, UpdatedAt: at}}
	return d.Normalize()
}
