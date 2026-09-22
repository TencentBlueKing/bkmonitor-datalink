// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package strategyhook 在 Alert 落库后标记待刷新策略，正式集合由控制面统一维护。
package strategyhook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
	"linkd/internal/activeindex"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
)

// Client 原子合并刷新提示；连接生命周期由装配方管理。
type Client interface {
	Eval(context.Context, string, []string, ...any) *redis.Cmd
}

// Config 定义输出前缀和超时；Redis 连接及 DB 由注入客户端管理。
type Config struct {
	// KeyPrefix 是集合前缀，集合继续按租户和策略隔离。
	KeyPrefix string
	// Timeout 限制提交刷新提示的操作。
	Timeout time.Duration
}

// Hook 只提交策略刷新提示，重复和乱序调用不会修改正式集合。
type Hook struct {
	client  Client
	prefix  string
	timeout time.Duration
}

// New 创建索引插件，不建立连接，也不接管客户端的关闭职责。
// 控制面完成集合更新后才向 KeyPrefix + ":changes" 发布通知。
func New(client Client, cfg Config) (*Hook, error) {
	if client == nil || activeindex.ValidatePrefix(cfg.KeyPrefix) != nil || cfg.Timeout <= 0 {
		return nil, fmt.Errorf("strategy hook requires client, valid prefix and positive timeout")
	}
	return &Hook{client: client, prefix: cfg.KeyPrefix, timeout: cfg.Timeout}, nil
}

// Execute 同步提交刷新提示；提示失败不阻止告警主流程，由控制面周期校准恢复。
// 随机 token 仅区分并发刷新请求，不参与业务身份和消息幂等。
func (h *Hook) Execute(ctx context.Context, input lifecycle.FinalHookInput) (lifecycle.FinalHookResult, error) {
	result := lifecycle.FinalHookResult{Name: "active-alert-by-strategy", Transport: "redis", Destination: h.prefix, MessageID: invocationID(input)}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := input.Alert.Validate(); err != nil {
		return result, fmt.Errorf("invalid alert for strategy hook")
	}
	if err := input.Cause.Validate(); err != nil {
		return result, fmt.Errorf("invalid cause for strategy hook")
	}
	strategy, skip, err := strategyID(input.Alert.Labels)
	if err != nil {
		return result, err
	}
	if skip {
		result.Skipped = true
		return result, nil
	}
	// 租户为强制 key 段；来源隔离仅由发布配置中的 prefix 决定。
	key := h.prefix + ":" + input.Alert.BKTenantID + ":" + strategy
	result.Destination = key
	call, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	scope := activeindex.Scope{BKTenantID: input.Alert.BKTenantID, StrategyID: strategy}
	if err := scope.Validate(); err != nil {
		return result, fmt.Errorf("invalid strategy scope")
	}
	err = h.client.Eval(call, activeindex.HintScript, activeindex.HintKeys(h.prefix), key, uuid.NewString(), 1000).Err()
	if err != nil {
		// Redis 服务端错误可能含服务端返回的敏感文本；仅保留可用于 errors.Is 的取消分类。
		if errors.Is(err, context.Canceled) {
			return result, fmt.Errorf("redis index canceled: %w", context.Canceled)
		}
		if errors.Is(err, context.DeadlineExceeded) || call.Err() == context.DeadlineExceeded {
			return result, fmt.Errorf("redis index timeout: %w", context.DeadlineExceeded)
		}
		return result, fmt.Errorf("redis index update failed")
	}
	return result, nil
}

func strategyID(labels domain.DimensionMap) (string, bool, error) {
	return activeindex.StrategyID(labels)
}

func invocationID(input lifecycle.FinalHookInput) string {
	// 固定字段数组避免拼接分隔符歧义，身份不依赖当前时间或进程内顺序。
	data, _ := json.Marshal([]string{"linkd:strategy-hook", input.Alert.BKTenantID, input.Alert.AlertID, input.Alert.UpdateAt.UTC().Format(time.RFC3339Nano), input.Cause.Type, input.Cause.ID, string(input.Alert.Status)})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
