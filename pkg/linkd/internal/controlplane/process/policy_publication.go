// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"log/slog"
	"time"

	"linkd/internal/controlplane/taskstate"
	"linkd/internal/policy"
	"linkd/internal/telemetry"
)

type policyRecoverer interface {
	RecoverPage(context.Context, string, int) (string, error)
}

// runPolicyPublication 每轮最多一页，存储 CAS 允许多控制面安全重试同一待发布版本。
// 一条配置的恢复错误不丢弃 Pending，也不永久阻塞其后的配置；下一遍扫描继续恢复。
func runPolicyPublication(ctx context.Context, service policyRecoverer, logger *slog.Logger, registry *taskstate.Registry, metrics *telemetry.Runtime) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	after := ""
	for {
		if ctx.Err() != nil {
			return nil
		}
		after = reconcilePolicyPublication(ctx, service, after, logger, registry, metrics)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func reconcilePolicyPublication(ctx context.Context, service policyRecoverer, after string, logger *slog.Logger, registry *taskstate.Registry, metrics *telemetry.Runtime) string {
	const id = "policy-publication"
	started := time.Now()
	registry.Begin(id, "", "")
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	next, err := service.RecoverPage(call, after, policy.MaxPageSize)
	code := ""
	failed := 0
	if err != nil {
		code = "policy_publication_recovery_failed"
		failed = 1
		if ctx.Err() == nil {
			logger.WarnContext(ctx, "policy publication recovery failed", "error_code", code)
		}
	}
	elapsed := time.Since(started)
	registry.Finish(ctx, id, "", "", elapsed, -1, failed, code)
	if ctx.Err() == nil {
		metrics.ControlPlaneTaskObserver(telemetry.ControlPlaneTaskPolicyPublication).RunFinished(ctx, elapsed, err == nil)
	}
	return next
}
