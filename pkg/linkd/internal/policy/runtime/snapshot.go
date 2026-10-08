// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package runtime

import (
	"context"
	"errors"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/store"
)

// Snapshotter 将加载的配置列表转为可持久化引用；网络/配置错误记录跳过，身份错误不能降级。
type Snapshotter struct{ Catalog *policy.Catalog }

// Snapshot 在 Redis 副作用前固定本次处理时间，不用来源时间混入防抖窗口。
func (s Snapshotter) Snapshot(ctx context.Context, event domain.Event, at time.Time) (*store.PolicyContext, error) {
	snapshot := &store.PolicyContext{EvaluatedAt: at, Releases: []store.PolicyReleaseRef{}}
	if s.Catalog == nil {
		snapshot.ReasonCode = "policy_load_failed"
		return snapshot, nil
	}
	policies, err := s.Catalog.Freeze(ctx, event.BKTenantID)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if errors.Is(err, policy.ErrAccess) || errors.Is(err, policy.ErrInvalid) {
		return nil, err
	}
	if err != nil {
		snapshot.ReasonCode = "policy_load_failed"
		return snapshot, nil
	}
	for _, item := range policies {
		if item.Release.TenantID != event.BKTenantID {
			return nil, policy.ErrAccess
		}
		snapshot.Releases = append(snapshot.Releases, store.PolicyReleaseRef{Kind: string(item.Release.Kind), ID: item.Release.ID, Version: item.Release.Version, Digest: item.Compiled.Summary.Digest})
	}
	return snapshot, snapshot.Validate()
}
