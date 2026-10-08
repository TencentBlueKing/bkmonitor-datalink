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
	"errors"
	"fmt"
	"maps"

	"linkd/internal/actiondelivery"
	"linkd/internal/domain"
	"linkd/internal/store"
)

// ActionRecorder 仅负责把原动作可靠入队；成功必须保证全部目标任务持久化且可被排序查询发现。
// 允许部分目标已成功后返回错误，重试必须按原版本/原因幂等，不得在这里等待远端处置完成。
type ActionRecorder interface {
	RecordAction(context.Context, domain.Alert, domain.AlertActionIntent) error
}

// WithActionRecorder 注入必需动作入队端口，错误向输入/控制任务传播，不沿用普通 Hook 的忽略策略。
func WithActionRecorder(recorder ActionRecorder) ProcessorOption {
	return func(p *Processor) { p.actionRecorder = recorder }
}

// WithInitialProjectionTargets 为新 Alert 绑定装配方启用的全局插件目标。
// map 值显式决定是否投递获准动作；已有 Alert 在整个生命周期保留原绑定，本选项不更新它。
func WithInitialProjectionTargets(targets map[string]bool) ProcessorOption {
	frozen := maps.Clone(targets)
	return func(p *Processor) { p.initialProjectionTargets = maps.Clone(frozen) }
}

func (p *Processor) initialProjection(ctx context.Context, event domain.Event) (domain.AlertProjection, error) {
	targets := p.initialProjectionTargets
	if err := ctx.Err(); err != nil {
		return domain.AlertProjection{}, err
	}

	projection := domain.AlertProjection{}
	if len(targets) > 16 {
		return projection, fmt.Errorf("projection target budget exceeded")
	}
	if len(targets) > 0 {
		projection.Targets = make(map[string]domain.ProjectionTargetState, len(targets))
	}
	for id, actions := range targets {
		if actions && p.actionRecorder == nil {
			return domain.AlertProjection{}, fmt.Errorf("selected action target requires reliable recorder")
		}
		projection.Targets[id] = domain.ProjectionTargetState{SourceVersion: event.EventSourceVersion, RequiredRevision: 1, ActionEnabled: actions}
	}
	if err := projection.Validate(1); err != nil {
		return domain.AlertProjection{}, err
	}
	return projection, nil
}

func freezeAction(a *domain.Alert, revision int64, cause AlertChangeCause, ready bool) error {
	if !ready {
		return nil
	}
	// 控制面变更尚未经过仓储版本计算；先按已读取的下一版冻结，CAS 再核对版本和水位。
	a.Revision = revision
	var err error
	a.Projection, err = a.Projection.RequireRevision(revision)
	if err != nil {
		return err
	}
	a.ActionPending, err = domain.NewAlertActionIntent(*a, cause.Type, cause.ID)
	if err != nil || a.ActionPending == nil {
		return err
	}
	// 在提交业务 CAS 前验证同一冻结快照能被实际协议编码；超预算不能提交成永远无法入队的意图。
	// 目标的格式/发布已由领域校验覆盖，完整快照预算只需对其中一个目标检查。
	for id := range a.ActionPending.Targets {
		_, err = actiondelivery.BuildRequest(*a, id, actiondelivery.Cause{Type: cause.Type, ID: cause.ID})
		break
	}
	return err
}

// FinishActionDelivery 在该 Alert 的 fingerprint lease 内补齐既有动作入队，不重新评估策略。
// 后台补扫和输入重试共用本用例；它只确认任务入队，不代表接收端已受理或业务已处置。
func (p *Processor) FinishActionDelivery(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	if ctx == nil || domain.ValidateIdentityPart("tenant", tenant, 64) != nil || id == "" || len(id) > domain.EntityIDMaxBytes {
		return store.StoredAlert{}, store.ErrInvalidArgument
	}
	current, err := p.getAlertCurrent(ctx, tenant, id)
	if err != nil {
		return store.StoredAlert{}, err
	}
	if current.Alert.BKTenantID != tenant || current.Alert.AlertID != id {
		return store.StoredAlert{}, store.ErrInvalidArgument
	}
	return p.finishActionIntent(ctx, current)
}

func (p *Processor) finishActionIntent(ctx context.Context, current store.StoredAlert) (store.StoredAlert, error) {
	tenant, id := current.Alert.BKTenantID, current.Alert.AlertID
	for range maxCASAttempts {
		if err := ctx.Err(); err != nil {
			return store.StoredAlert{}, err
		}
		if current.Version.IsZero() || current.Alert.Validate() != nil || current.Alert.BKTenantID != tenant || current.Alert.AlertID != id {
			return store.StoredAlert{}, store.ErrInvalidArgument
		}
		intent := current.Alert.ActionPending
		if intent == nil {
			return current, nil
		}
		if p.actionRecorder == nil {
			return store.StoredAlert{}, fmt.Errorf("durable action recorder is not configured")
		}
		if err := p.actionRecorder.RecordAction(ctx, current.Alert.Clone(), *intent.Clone()); err != nil {
			return store.StoredAlert{}, fmt.Errorf("record alert action: %w", err)
		}
		next := current.Alert.Clone()
		next.ActionPending = nil
		updated, err := p.compareAndSetAlert(ctx, current, next)
		if errors.Is(err, store.ErrVersionConflict) {
			current, err = p.getAlertCurrent(ctx, current.Alert.BKTenantID, current.Alert.AlertID)
			if err != nil {
				return store.StoredAlert{}, err
			}
			continue
		}
		if err != nil {
			return store.StoredAlert{}, err
		}
		if err := p.cacheMergeSnapshot(ctx, updated); err != nil {
			return store.StoredAlert{}, err
		}
		return updated, nil
	}
	return store.StoredAlert{}, store.ErrVersionConflict
}
