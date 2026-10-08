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
	"time"

	"linkd/internal/config"
	"linkd/internal/lifecycle"
	"linkd/internal/runtimeconfig"
	"linkd/internal/store"
	"linkd/internal/telemetry"
)

// MergeOperator 为控制面合并步骤复用来源 Hook、实时仓储及 Worker fingerprint lease。
// 成员释放/建联与父就绪共享最多四个并发、单项十秒的预算，不运行 Cleaner 或 Enrich。
type MergeOperator struct {
	slots chan struct{}
	run   func(context.Context, string, string, string, string) (store.StoredAlert, error)
}

// NewMergeOperator 注入权威关系读取；具体裁决和步骤选择仍由控制面合并任务负责。
func NewMergeOperator(cfg config.Config, sources closeSourceReader, relations lifecycle.MergeRelationReader, severity *runtimeconfig.Severity, logger *slog.Logger, metrics *telemetry.Runtime) *MergeOperator {
	return &MergeOperator{slots: make(chan struct{}, 4), run: func(ctx context.Context, kind, tenant, id, reference string) (result store.StoredAlert, err error) {
		err = withAlertProcessor(ctx, cfg, sources, severity, logger, metrics, tenant, id, func(store.Repository) []lifecycle.ProcessorOption {
			return []lifecycle.ProcessorOption{lifecycle.WithMergeRelations(relations)}
		}, func(p *lifecycle.Processor) error {
			var e error
			switch kind {
			case "finish":
				result, e = p.FinishMergeChanges(ctx, tenant, id)
			case "release":
				result, e = p.ReleaseMergeWindow(ctx, tenant, id, reference)
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
	}}
}

// FinishMergeChanges 在成员或父的租约内补齐控制操作意图，不凭扫描快照重放业务操作。
func (r *MergeOperator) FinishMergeChanges(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	return r.execute(ctx, "finish", tenant, id, "")
}

func (r *MergeOperator) execute(ctx context.Context, kind, tenant, id, reference string) (store.StoredAlert, error) {
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	case <-ctx.Done():
		return store.StoredAlert{}, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return r.run(ctx, kind, tenant, id, reference)
}

// ReleaseMergeWindow 完成指定窗口释放，基础存储、取消和租约冲突交由裁决重试。
func (r *MergeOperator) ReleaseMergeWindow(ctx context.Context, tenant, id, window string) (store.StoredAlert, error) {
	return r.execute(ctx, "release", tenant, id, window)
}

// LinkMergeMember 在成员租约内确认真实父并写入成员关系。
func (r *MergeOperator) LinkMergeMember(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
	return r.execute(ctx, "member_link", tenant, id, relation)
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
