// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycleprocess

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/policy/redisstate"
	policyruntime "linkd/internal/policy/runtime"
	"linkd/internal/runtimeconfig"
	"linkd/internal/store"
	"linkd/internal/telemetry"
)

// MergeOperator 为控制面合并步骤复用来源 Hook、实时仓储及 Worker fingerprint lease。
// 成员释放/建联与父就绪共享配置的并发名额、单项十秒预算，不运行 Cleaner 或 Enrich。
type MergeOperator struct {
	logger          *slog.Logger
	resources       *AlertProcessorResources
	metrics         *telemetry.Runtime
	memberBatchSize int
	sources         closeSourceReader
	withProcessor   func(context.Context, store.StoredAlert, func(*lifecycle.Processor) error) error
	slots           chan struct{}
	run             func(context.Context, string, string, string, string) (store.StoredAlert, error)
	prepared        func(context.Context, string, string, domain.MergeRelation, store.StoredAlert) (store.StoredAlert, error)
	linkPrepared    func(context.Context, string, string, domain.MergeRelation) (store.StoredAlert, error)
}

// UseResources 在启动合并任务前注入控制面共用连接。
func (r *MergeOperator) UseResources(resources *AlertProcessorResources) { r.resources = resources }

// NewMergeOperator 注入权威关系读取；具体裁决和步骤选择仍由控制面合并任务负责。
func NewMergeOperator(cfg config.Config, sources closeSourceReader, relations lifecycle.MergeRelationReader, severity *runtimeconfig.Severity, logger *slog.Logger, metrics *telemetry.Runtime, loaders ...*policyruntime.Suppressor) *MergeOperator {
	withProcessor := func(ctx context.Context, tenant, id string, run func(*lifecycle.Processor) error, identity ...store.StoredAlert) error {
		return withAlertProcessor(ctx, cfg, sources, severity, logger, metrics, tenant, id, func(repo store.Repository, state *redisstate.Store) []lifecycle.ProcessorOption {
			opts := []lifecycle.ProcessorOption{lifecycle.WithMergeRelations(relations)}
			if len(loaders) > 0 && loaders[0] != nil {
				current := repo.GetAlert
				if reader, ok := repo.(store.LifecycleAlertStore); ok {
					current = reader.GetAlertCurrent
				}
				opts = append(opts, lifecycle.WithDependencyRegistry(&policyruntime.Shielder{Loader: loaders[0], Index: state, Dependency: state, Hints: state, CurrentAlert: current, Events: repo.GetEvent, Observer: metrics, Logger: logger}))
			}
			return opts
		}, run, identity...)
	}
	return &MergeOperator{logger: logger, metrics: metrics, sources: sources, memberBatchSize: cfg.MergeControlSettings().MemberBatchSize, withProcessor: func(ctx context.Context, identity store.StoredAlert, run func(*lifecycle.Processor) error) error {
		return withProcessor(ctx, identity.Alert.BKTenantID, identity.Alert.AlertID, run, identity)
	}, slots: make(chan struct{}, cfg.MergeControlSettings().Concurrency), run: func(ctx context.Context, kind, tenant, id, reference string) (result store.StoredAlert, err error) {
		err = withProcessor(ctx, tenant, id, func(p *lifecycle.Processor) error {
			var e error
			action, reason, _ := strings.Cut(kind, ":")
			switch action {
			case "finish":
				result, e = p.FinishMergeChanges(ctx, tenant, id)
			case "release":
				if reason == "" {
					reason = "window_released"
				}
				result, e = p.ReleaseMergeWindowWithReason(ctx, tenant, id, reference, reason)
			case "member_link":
				result, e = p.LinkMergeMember(ctx, tenant, id, reference)
			case "parent_ready":
				result, e = p.ReadyMergeParent(ctx, tenant, id, reference)
			case "member_unlink":
				result, e = p.UnlinkMergeMember(ctx, tenant, id, reference)
			case "parent_recover":
				result, e = p.RecoverMergeParent(ctx, tenant, id, reference)
			default:
				e = fmt.Errorf("invalid merge operation")
			}
			return e
		})
		return result, err
	}, prepared: func(ctx context.Context, tenant, id string, relation domain.MergeRelation, parent store.StoredAlert) (result store.StoredAlert, err error) {
		err = withProcessor(ctx, tenant, id, func(p *lifecycle.Processor) error {
			var e error
			result, e = p.UnlinkMergeMemberPrepared(ctx, tenant, id, relation, parent)
			return e
		})
		return result, err
	}, linkPrepared: func(ctx context.Context, tenant, id string, relation domain.MergeRelation) (result store.StoredAlert, err error) {
		err = withProcessor(ctx, tenant, id, func(p *lifecycle.Processor) error {
			var e error
			result, e = p.LinkMergeMemberPrepared(ctx, tenant, id, relation)
			return e
		})
		return result, err
	}}
}

// FinishMergeChanges 在成员或父的租约内补齐控制操作意图，不凭扫描快照重放业务操作。
func (r *MergeOperator) FinishMergeChanges(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	return r.execute(ctx, "finish", tenant, id, "")
}

func (r *MergeOperator) execute(ctx context.Context, kind, tenant, id, reference string) (store.StoredAlert, error) {
	return r.withSlot(ctx, func(call context.Context) (store.StoredAlert, error) {
		return r.run(call, kind, tenant, id, reference)
	})
}

func (r *MergeOperator) withSlot(ctx context.Context, run func(context.Context) (store.StoredAlert, error)) (store.StoredAlert, error) {
	started := time.Now()
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	case <-ctx.Done():
		return store.StoredAlert{}, ctx.Err()
	}
	store.ObserveBatchPhase(ctx, "member.slot_wait", time.Since(started))
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return run(r.resources.context(ctx))
}

// ReleaseMergeWindow 完成指定窗口释放，基础存储、取消和租约冲突交由裁决重试。
func (r *MergeOperator) ReleaseMergeWindow(ctx context.Context, tenant, id, window string) (store.StoredAlert, error) {
	return r.execute(ctx, "release", tenant, id, window)
}

// ReleaseMergeWindowWithReason 将确认的业务原因带入同一 fingerprint lease，不在输出重试时重新裁决。
func (r *MergeOperator) ReleaseMergeWindowWithReason(ctx context.Context, tenant, id, window, reason string) (store.StoredAlert, error) {
	return r.execute(ctx, "release:"+reason, tenant, id, window)
}

// LinkMergeMember 在成员租约内确认真实父并写入成员关系。
func (r *MergeOperator) LinkMergeMember(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
	return r.execute(ctx, "member_link", tenant, id, relation)
}

// LinkMergeMemberPrepared 复用本批固定关系，沿原执行预算和每个子告警的锁实时核对活动父。
func (r *MergeOperator) LinkMergeMemberPrepared(ctx context.Context, tenant, id string, relation domain.MergeRelation) (store.StoredAlert, error) {
	return r.withSlot(ctx, func(call context.Context) (store.StoredAlert, error) {
		return r.linkPrepared(call, tenant, id, relation)
	})
}

// ReadyMergeParent 在父租约内确认已完成关系并按资格放行。
func (r *MergeOperator) ReadyMergeParent(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
	return r.execute(ctx, "parent_ready", tenant, id, relation)
}

// RecoverMergeParent 在父租约内完成全部子终态检查和幂等系统恢复。
func (r *MergeOperator) RecoverMergeParent(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
	return r.execute(ctx, "parent_recover", tenant, id, relation)
}

// UnlinkMergeMember 在成员租约内解除已终结父的关系，不改变成员生命周期或生成新处置。
func (r *MergeOperator) UnlinkMergeMember(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
	return r.execute(ctx, "member_unlink", tenant, id, relation)
}

// UnlinkMergeMemberPrepared 与普通解除共用配置的执行名额和单成员租约，复用批内已固定的父/关系事实。
func (r *MergeOperator) UnlinkMergeMemberPrepared(ctx context.Context, tenant, id string, relation domain.MergeRelation, parent store.StoredAlert) (store.StoredAlert, error) {
	return r.withSlot(ctx, func(call context.Context) (store.StoredAlert, error) {
		return r.prepared(call, tenant, id, relation, parent)
	})
}
