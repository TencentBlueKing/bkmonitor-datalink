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

	"linkd/internal/domain"
)

// AlertChangePublisher 只发布轻量通知，发布失败不回滚已经保存的业务事实。
type AlertChangePublisher interface {
	Publish(context.Context, domain.AlertChange) error
}

// WithAlertChangePublisher 注入通知出口，Lifecycle 不依赖 KAC 的连接、任务或协议。
func WithAlertChangePublisher(publisher AlertChangePublisher) ProcessorOption {
	return func(p *Processor) { p.publisher = publisher }
}

func (p *Processor) publishChange(ctx context.Context, a domain.Alert, cause AlertChangeCause, ready bool) {
	if p.publisher == nil {
		return
	}
	change := domain.NewAlertChange(a, domain.AlertActionCause{Type: cause.Type, ID: cause.ID}, ready)
	if err := p.publisher.Publish(ctx, change); err != nil {
		p.logger.WarnContext(ctx, "alert change enqueue failed", "module", "lifecycle", "operation", "publish_change", "error", err, "event_source_id", a.EventSourceID, "bk_tenant_id", a.BKTenantID, "alert_id", a.AlertID, "cause_id", cause.ID, "error_code", "queue_publish_failed")
	}
}
