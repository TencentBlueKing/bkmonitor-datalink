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
	"errors"
	"time"

	"linkd/internal/domain"
)

// ProjectionAcknowledgment 只描述已持久化回执对应的同步水位，不携带业务快照。
type ProjectionAcknowledgment struct {
	TenantID, AlertID, TargetID string
	SourceVersion, Revision     int64
	At                          time.Time
}

// ProjectionAcknowledger 在同一次条件写中推进目标水位与待办标记。
type ProjectionAcknowledger interface {
	AcknowledgeProjection(context.Context, ProjectionAcknowledgment) error
}

// Validate 拒绝跨租户身份、非法版本和空确认时间。
func (a ProjectionAcknowledgment) Validate() error {
	if domain.ValidateIdentityPart("tenant", a.TenantID, 64) != nil || a.AlertID == "" || len(a.AlertID) > domain.EntityIDMaxBytes || domain.ValidateIdentityPart("target", a.TargetID, 64) != nil || a.SourceVersion < 1 || a.Revision < 1 || a.SourceVersion >= 1<<53 || a.Revision >= 1<<53 || a.At.IsZero() {
		return ErrInvalidArgument
	}
	return nil
}

// AcknowledgeProjection 为没有局部更新能力的后端保留原单对象 CAS 语义。
func AcknowledgeProjection(ctx context.Context, repository Repository, ack ProjectionAcknowledgment) error {
	if err := ack.Validate(); err != nil {
		return err
	}
	for range 4 {
		row, err := repository.GetAlert(ctx, ack.TenantID, ack.AlertID)
		if err != nil {
			return err
		}
		next := row.Alert.Clone()
		watermarks, changed, err := next.Projection.Acknowledge(ack.TargetID, ack.SourceVersion, ack.Revision, ack.At)
		if err != nil || !changed {
			return err
		}
		next.Projection = watermarks
		_, err = repository.CompareAndSetAlert(ctx, ack.TenantID, ack.AlertID, row.Version, next)
		if errors.Is(err, ErrVersionConflict) {
			continue
		}
		return err
	}
	return ErrVersionConflict
}
