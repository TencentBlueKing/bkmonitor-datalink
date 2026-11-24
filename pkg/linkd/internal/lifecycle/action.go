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
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
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
	started := time.Now()
	defer func() { store.ObserveBatchPhase(ctx, "member.enqueue", time.Since(started)) }()
	change := domain.NewAlertChange(a, domain.AlertActionCause{Type: cause.Type, ID: cause.ID}, ready)
	if err := p.publisher.Publish(ctx, change); err != nil {
		p.logger.WarnContext(ctx, "alert change enqueue failed", "module", "lifecycle", "operation", "publish_change", "error", err, "event_source_id", a.EventSourceID, "bk_tenant_id", a.BKTenantID, "alert_id", a.AlertID, "cause_id", cause.ID, "error_code", "queue_publish_failed")
	}
}

func (p *Processor) publishCloseChanges(ctx context.Context, alerts []domain.Alert, commands []CloseAlertCommand) {
	if p.publisher == nil {
		return
	}
	batch, ok := p.publisher.(interface {
		PublishBatch(context.Context, []domain.AlertChange) []error
	})
	if !ok {
		for i, a := range alerts {
			kind := AlertChangeCauseUserOperation
			if commands[i].OperatorKind == domain.OperatorKindSystem {
				kind = AlertChangeCauseSystemOperation
			}
			p.publishChange(ctx, a, AlertChangeCause{Type: kind, ID: commands[i].OperationID}, a.Admission.AdmittedAt != nil)
		}
		return
	}
	started := time.Now()
	defer func() { store.ObserveBatchPhase(ctx, "batch.enqueue", time.Since(started)) }()
	changes := make([]domain.AlertChange, len(alerts))
	for i, a := range alerts {
		kind := AlertChangeCauseUserOperation
		if commands[i].OperatorKind == domain.OperatorKindSystem {
			kind = AlertChangeCauseSystemOperation
		}
		changes[i] = domain.NewAlertChange(a, domain.AlertActionCause{Type: kind, ID: commands[i].OperationID}, a.Admission.AdmittedAt != nil)
	}
	errs := batch.PublishBatch(ctx, changes)
	for i, err := range errs {
		if err != nil {
			a := alerts[i]
			p.logger.WarnContext(ctx, "alert change enqueue failed", "module", "lifecycle", "operation", "publish_change", "error", err, "event_source_id", a.EventSourceID, "bk_tenant_id", a.BKTenantID, "alert_id", a.AlertID, "cause_id", commands[i].OperationID, "error_code", "queue_publish_failed")
		}
	}
}
