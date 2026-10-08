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
	"log/slog"
	"time"

	"linkd/internal/config"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	policyruntime "linkd/internal/policy/runtime"
	"linkd/internal/runtimeconfig"
	"linkd/internal/store"
	"linkd/internal/telemetry"
)

// ManualShieldBinder 为快捷屏蔽提供固定并发和 Lifecycle 租约边界。
type ManualShieldBinder struct {
	slots chan struct{}
	run   func(context.Context, lifecycle.ShieldCommand) (lifecycle.ShieldCommandResult, error)
}

// NewManualShieldBinder 不启动后台工作，状态写入与普通 Lifecycle 共用 CAS 和输出补齐。
func NewManualShieldBinder(cfg config.Config, sources closeSourceReader, policies *policy.Service, severity *runtimeconfig.Severity, logger *slog.Logger, metrics *telemetry.Runtime) *ManualShieldBinder {
	return &ManualShieldBinder{slots: make(chan struct{}, 4), run: func(ctx context.Context, c lifecycle.ShieldCommand) (result lifecycle.ShieldCommandResult, err error) {
		err = withAlertProcessor(ctx, cfg, sources, severity, logger, metrics, c.TenantID, c.AlertID, func(store.Repository) []lifecycle.ProcessorOption {
			return []lifecycle.ProcessorOption{lifecycle.WithShieldEvaluator(&policyruntime.Shielder{Loader: &policyruntime.Suppressor{Releases: policies}, Logger: logger, Observer: metrics})}
		}, func(p *lifecycle.Processor) error { var e error; result, e = p.BindShield(ctx, c); return e })
		return result, err
	}}
}

// BindShield 满额立即背压；取消或失败可能发生在状态保存之后，调用方应复用原命令重试。
func (b *ManualShieldBinder) BindShield(ctx context.Context, c lifecycle.ShieldCommand) (lifecycle.ShieldCommandResult, error) {
	if ctx == nil || c.Validate() != nil {
		return lifecycle.ShieldCommandResult{}, store.ErrInvalidArgument
	}
	select {
	case <-ctx.Done():
		return lifecycle.ShieldCommandResult{}, ctx.Err()
	default:
	}
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	default:
		return lifecycle.ShieldCommandResult{}, policy.ErrPreviewCapacity
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return b.run(call, c)
}
