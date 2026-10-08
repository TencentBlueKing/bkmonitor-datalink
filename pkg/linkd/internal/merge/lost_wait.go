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
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/store"
)

// WindowReader 读取完整窗口或明确的缓存缺失；超时和不完整读取不能返回 found=false,nil。
type WindowReader interface {
	ReadMergeWindow(context.Context, string, string) (redisstate.MergeWindow, bool, error)
}

// ReleaseLostWait 只释放原截止时间已到、Redis 明确丢失且没有持久化裁决的等待。
// 调用方必须持有该租户/窗口的控制任务租约，使裁决创建、建联和丢失释放串行；releaser 另取成员 fingerprint lease。
// true 表示该成员已完成本次清理，false 表示仍由窗口/裁决或原截止时间保护，不能据此开放处置。
func (j *Journal) ReleaseLostWait(ctx context.Context, current store.StoredAlert, window string, windows WindowReader, releaser MemberReleaser, at time.Time) (bool, error) {
	if windows == nil || releaser == nil || current.Version.IsZero() || current.Alert.Validate() != nil || at.IsZero() {
		return false, policy.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	a := current.Alert
	if _, err := domain.MergeDecisionID(a.BKTenantID, window); err != nil {
		return false, policy.ErrInvalid
	}
	if a.Status != domain.AlertStatusActive || a.Merge == nil || a.Merge.Role != "original" {
		return false, nil
	}
	var wait *domain.MergeWait
	for _, w := range a.Merge.Pending {
		if w.WindowID == window {
			v := w.Clone()
			wait = &v
			break
		}
	}
	if wait == nil {
		return false, nil
	}
	if _, err := j.GetByWindow(ctx, a.BKTenantID, window); err == nil {
		return false, nil
	} else if !errors.Is(err, policy.ErrNotFound) {
		return false, err
	}
	if at.Before(wait.Deadline) {
		return false, nil
	}
	w, found, err := windows.ReadMergeWindow(ctx, a.BKTenantID, window)
	if err != nil {
		return false, err
	}
	if found {
		if w.TenantID != a.BKTenantID || w.ID != window || w.Policy != wait.Policy || w.GroupKey != wait.GroupKey {
			return false, policy.ErrAccess
		}
		return false, nil
	}
	released, err := releaser.ReleaseMergeWindow(ctx, a.BKTenantID, a.AlertID, window)
	// 这里的成员曾真实落库，与失败裁决中的 reserved 候选不同；缺失不能被解释为清理成功。
	if errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	if err := validateReleasedMember(a.BKTenantID, a.AlertID, window, released, err); err != nil {
		return false, err
	}
	return true, nil
}
