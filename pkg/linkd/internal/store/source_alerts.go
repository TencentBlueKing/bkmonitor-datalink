// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package store

import (
	"context"
	"fmt"

	"linkd/internal/domain"
)

// SourceAlertQuery 只读枚举指定租户、来源的原始告警，空 Status 表示全部保留状态。
// After 为 AlertID 排序锚点；查询不承诺跨页事务快照。
type SourceAlertQuery struct {
	TenantID      string
	EventSourceID string
	Status        domain.AlertStatus
	After         string
	Limit         int
}

// SourceAlertPage 保存来源告警的有界分页，包含原始事实和下一页 AlertID 锚点。
type SourceAlertPage struct {
	Alerts []StoredAlert
	Next   string
}

// SourceAlertReader 供来源恢复轮询读取来源事实，不依赖 KAC 兼容投影。
type SourceAlertReader interface {
	ListSourceAlerts(context.Context, SourceAlertQuery) (SourceAlertPage, error)
}

// Validate 拒绝无作用域和无界扫描。
func (q SourceAlertQuery) Validate() error {
	if err := ValidateActiveAlertPage(q.TenantID, q.After, q.Limit); err != nil {
		return err
	}
	if err := domain.ValidateIdentityPart("event_source_id", q.EventSourceID, 32); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	switch q.Status {
	case "", domain.AlertStatusActive, domain.AlertStatusRecovered, domain.AlertStatusClosed:
		return nil
	}
	return fmt.Errorf("%w: source alert status", ErrInvalidArgument)
}

// Matches 校验返回快照仍在请求的租户、来源、状态和排序范围内。
func (q SourceAlertQuery) Matches(a domain.Alert) bool {
	return a.BKTenantID == q.TenantID && a.EventSourceID == q.EventSourceID && a.AlertID > q.After && (q.Status == "" || a.Status == q.Status)
}
