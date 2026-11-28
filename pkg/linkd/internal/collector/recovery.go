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
	"errors"
	"fmt"
	"log/slog"
	"time"

	"linkd/internal/pluginruntime"
)

// PendingRequests 提供来源范围内的待处理身份、首次输入及终结状态。
type PendingRequests interface {
	PendingIDs(context.Context, string, string, int64) ([]string, error)
	LoadRequest(context.Context, string, string, string) (pluginruntime.Request, error)
	LoadResult(context.Context, pluginruntime.Request) (*StoredResult, error)
}

// Recovery 编排已有来源授权下的持久请求恢复，不自行获取租约。
type Recovery struct {
	Pending   PendingRequests
	Replay    func(context.Context, pluginruntime.Request) (pluginruntime.Result, error)
	BatchSize int64
	Timeout   time.Duration
	Logger    *slog.Logger
}

// Drain 串行处理待恢复请求，未终结错误返回后，调用方不得越过它发起新周期。
func (r *Recovery) Drain(ctx context.Context, tenant, source string) error {
	for {
		count, err := r.Run(ctx, tenant, source)
		if err != nil {
			return err
		}
		if int64(count) < r.BatchSize {
			return nil
		}
	}
}

// Run 在调用方持有来源执行授权期间，按首次时间串行恢复一批请求。
// 每条请求使用独立预算；返回已终结数量。明确插件失败终结后继续，未终结错误立即
// 返回，避免越过旧请求提交新游标。此方法不获取授权、不循环等待、不接收新任务。
func (r *Recovery) Run(ctx context.Context, tenant, source string) (int, error) {
	pending, limit, timeout, logger := r.Pending, r.BatchSize, r.Timeout, r.Logger
	if limit <= 0 || timeout <= 0 {
		return 0, fmt.Errorf("positive recovery batch and timeout required")
	}
	listCtx, cancelList := context.WithTimeout(ctx, timeout)
	ids, err := pending.PendingIDs(listCtx, tenant, source, limit)
	cancelList()
	if err != nil {
		return 0, fmt.Errorf("list pending source requests: %w", err)
	}
	finished := 0
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return finished, err
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		request, loadErr := pending.LoadRequest(callCtx, tenant, source, id)
		if loadErr != nil {
			cancel()
			return finished, fmt.Errorf("load pending source request %s: %w", id, loadErr)
		}
		_, replayErr := r.Replay(callCtx, request)
		if errors.Is(replayErr, ErrAuthorityLost) {
			cancel()
			return finished, replayErr
		}
		if replayErr != nil {
			logger.ErrorContext(ctx, "source request recovery failed", "module", "collector", "operation", "recover", "error", replayErr, "bk_tenant_id", tenant, "event_source_id", source, "event_source_version", request.SourceVersion, "request_id", id)
			// 不能仅凭错误类型跳过：插件失败的终结记录本身也可能写入失败。
			stored, statusErr := pending.LoadResult(callCtx, request)
			cancel()
			if statusErr != nil || stored == nil || !stored.Completed {
				if statusErr != nil {
					return finished, fmt.Errorf("read source recovery outcome: %w", statusErr)
				}
				return finished, replayErr
			}
		} else {
			cancel()
		}
		finished++
	}
	return finished, nil
}
