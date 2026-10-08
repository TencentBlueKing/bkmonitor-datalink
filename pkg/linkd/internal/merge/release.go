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
	"context"
	"errors"
	"fmt"
	"time"

	"linkd/internal/policy"
	"linkd/internal/store"
)

// MemberReleaser 必须取得与事件处理相同的 fingerprint lease，再实时读取成员并释放指定窗口。
// 成功仅在状态、流水和当前输出步骤完成后返回；找不到从未落库的候选可返回 store.ErrNotFound。
type MemberReleaser interface {
	ReleaseMergeWindow(context.Context, string, string, string) (store.StoredAlert, error)
}

// ReleaseStep 处理失败裁决中的一个候选，再 CAS 推进持久化前缀；最多执行一个成员操作。
// 成员写成功但游标写失败时复用同一窗口重试。不能以另一个窗口已释放或整个 Alert 终态代替身份核对。
func (j *Journal) ReleaseStep(ctx context.Context, tenant, id string, releaser MemberReleaser, at time.Time) (StoredDecision, error) {
	if releaser == nil {
		return StoredDecision{}, policy.ErrInvalid
	}
	current, err := j.Get(ctx, tenant, id)
	if err != nil {
		return StoredDecision{}, err
	}
	if current.Decision.Progress.Phase == "completed" {
		return current, nil
	}
	d := current.Decision
	if at.IsZero() || at.Before(d.Progress.UpdatedAt) {
		return StoredDecision{}, policy.ErrInvalid
	}
	if d.Progress.Phase != "releasing" {
		return StoredDecision{}, policy.ErrConflict
	}
	if d.Progress.MemberOffset == len(d.WaitMemberIDs) {
		return j.Complete(ctx, current, nil, at)
	}
	member := d.WaitMemberIDs[d.Progress.MemberOffset]
	released, err := releaser.ReleaseMergeWindow(ctx, tenant, member, d.WindowID)
	if err := validateReleasedMember(tenant, member, d.WindowID, released, err); err != nil {
		return StoredDecision{}, err
	}

	// reserved 候选可能从未创建 Alert；实时确认缺失时没有可放行的对象，仍可清理本次等待进度。
	updated, err := j.AdvanceMembers(ctx, current, d.Progress.MemberOffset+1, at)
	if err != nil {
		return StoredDecision{}, err
	}
	if updated.Decision.Progress.MemberOffset == len(d.WaitMemberIDs) {
		return j.Complete(ctx, updated, nil, at)
	}
	return updated, nil
}

// validateReleasedMember 只承认指定成员、指定窗口的完成事实，供成功裁决的落选成员与失败裁决共用。
func validateReleasedMember(tenant, member, window string, released store.StoredAlert, err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if released.Version.IsZero() || released.Alert.Validate() != nil || released.Alert.MergeChange != nil || released.Alert.PolicyChange != nil {
		return fmt.Errorf("incomplete merge member release")
	}
	if released.Alert.BKTenantID != tenant || released.Alert.AlertID != member {
		return policy.ErrAccess
	}
	if released.Alert.Merge != nil {
		for _, wait := range released.Alert.Merge.Pending {
			if wait.WindowID == window {
				return fmt.Errorf("merge member still waits for released window")
			}
		}
	}
	return nil
}
