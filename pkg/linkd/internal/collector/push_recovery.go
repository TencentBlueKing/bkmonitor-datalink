// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"linkd/internal/pluginruntime"
)

// PushRequestRef 只包含持久接收身份，扫描不读取业务正文或插件凭据。
type PushRequestRef struct{ TenantID, SourceID, RequestID string }

func (r *RedisReceipts) pushPendingKey() string { return r.prefix + ":collector:push-pending" }

// ScanPushPending 使用 Redis 游标遍历全部待处理推送，包括停用/删除来源的已接受请求。
// count 是 Redis SCAN 提示，不是精确返回上限；执行器另按 BatchSize 限制每轮处理量。
func (r *RedisReceipts) ScanPushPending(ctx context.Context, cursor uint64, count int64) ([]PushRequestRef, uint64, error) {
	values, next, err := r.client.ZScan(ctx, r.pushPendingKey(), cursor, "", count).Result()
	if err != nil {
		return nil, cursor, err
	}
	refs := make([]PushRequestRef, 0, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		var identity [3]string
		if err := json.Unmarshal([]byte(values[i]), &identity); err != nil {
			return nil, cursor, fmt.Errorf("decode push pending identity: %w", err)
		}
		refs = append(refs, PushRequestRef{identity[0], identity[1], identity[2]})
	}
	return refs, next, nil
}

// PushPending 读取已接受推送的索引与固定输入；不提供来源配置或执行授权。
type PushPending interface {
	ScanPushPending(context.Context, uint64, int64) ([]PushRequestRef, uint64, error)
	LoadRequest(context.Context, string, string, string) (pluginruntime.Request, error)
}

// PushRecovery 在当前控制面任务上下文中分批恢复推送，Replay 必须使用同进程执行登记和领导权写入保护。
// 单实例串行调用 Run/Step；它不启动 Python 定时器，不获取新的分布式锁。
type PushRecovery struct {
	Pending   PushPending
	Replay    func(context.Context, pluginruntime.Request) (pluginruntime.Result, error)
	BatchSize int64
	Timeout   time.Duration
	Logger    *slog.Logger
	cursor    uint64
	buffered  []PushRequestRef
}

// Step 最多处理一批请求；忙请求留在持久索引，后续扫描再试，不阻塞其他来源。
func (r *PushRecovery) Step(ctx context.Context) error {
	if r.BatchSize <= 0 || r.Timeout <= 0 {
		return fmt.Errorf("positive push recovery batch and timeout required")
	}
	if len(r.buffered) == 0 {
		call, cancel := context.WithTimeout(ctx, r.Timeout)
		refs, next, err := r.Pending.ScanPushPending(call, r.cursor, r.BatchSize)
		cancel()
		if err != nil {
			return fmt.Errorf("scan pending pushes: %w", err)
		}
		r.buffered, r.cursor = refs, next
	}
	count := min(int64(len(r.buffered)), r.BatchSize)
	for range count {
		if err := ctx.Err(); err != nil {
			return err
		}
		ref := r.buffered[0]
		r.buffered = r.buffered[1:]
		call, cancel := context.WithTimeout(ctx, r.Timeout)
		request, err := r.Pending.LoadRequest(call, ref.TenantID, ref.SourceID, ref.RequestID)
		if err == nil {
			_, err = r.Replay(call, request)
		}
		cancel()
		if errors.Is(err, ErrAuthorityLost) {
			return err
		}
		if err != nil && !errors.Is(err, ErrExecutionBusy) && ctx.Err() == nil {
			r.Logger.ErrorContext(ctx, "push recovery failed", "module", "collector", "operation", "recover_push", "error", err,
				"bk_tenant_id", ref.TenantID, "event_source_id", ref.SourceID, "request_id", ref.RequestID)
		}
	}
	return nil
}

// Run 启动时立即恢复一批，此后每五秒推进一批；授权丢失使整个控制面任务组退出。
func (r *PushRecovery) Run(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if err := r.Step(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, ErrAuthorityLost) {
				return err
			}
			r.Logger.ErrorContext(ctx, "push recovery scan failed", "module", "collector", "operation", "scan_push", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
