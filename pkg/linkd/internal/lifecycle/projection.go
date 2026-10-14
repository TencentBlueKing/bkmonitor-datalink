// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"

	"linkd/internal/store"
)

// AlertProjector 同步投影已保存的 Alert；失败由实现入队有限补偿，不能撤销业务写入。
type AlertProjector interface {
	Project(context.Context, store.StoredAlert) (store.StoredAlert, error)
}

// WithAlertProjector 为业务写入注入同步投影，不用于确认元数据更新。
func WithAlertProjector(projector AlertProjector) ProcessorOption {
	return func(p *Processor) { p.projector = projector }
}

func (p *Processor) projectAlert(ctx context.Context, row store.StoredAlert) store.StoredAlert {
	if p.projector == nil || !row.Alert.Projection.Pending() {
		return row
	}
	defer p.measureStep(phaseHook, "projection")()
	next, err := p.projector.Project(ctx, row)
	if err != nil {
		p.logger.WarnContext(ctx, "KAC inline projection incomplete", "bk_tenant_id", row.Alert.BKTenantID, "alert_id", row.Alert.AlertID, "revision", row.Alert.Revision)
		return row
	}
	if next.Version.IsZero() || next.Alert.BKTenantID != row.Alert.BKTenantID || next.Alert.AlertID != row.Alert.AlertID || next.Alert.Revision < row.Alert.Revision {
		return row
	}
	return next
}
