// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package strategyhook 将最终 Alert 状态投影为按租户和策略隔离的 Redis set。
package strategyhook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
)

// Client 原子执行集合修改及条件发布；连接生命周期由装配方管理。
type Client interface {
	Eval(context.Context, string, []string, ...any) *redis.Cmd
}

// Config 定义输出目标和默认启用的集合变更通知；Database 用于区分不按 DB 隔离的 Pub/Sub 消息。
type Config struct {
	// KeyPrefix 是集合前缀，集合继续按租户和策略隔离。
	KeyPrefix string
	// Database 必须与注入客户端选择的逻辑 DB 一致。
	Database int
	// Timeout 同时限制集合修改及发布操作。
	Timeout time.Duration
}

// Hook 对 active 执行 SADD，对 recovered/closed 执行 SREM，不设置 TTL。
// 同一前缀下不同来源共用成员，没有引用计数、乱序保护或失败补偿。
type Hook struct {
	client   Client
	prefix   string
	timeout  time.Duration
	channel  string
	database int
}

// New 创建索引插件，不建立连接，也不接管客户端的关闭职责。
// 通知始终启用，channel 固定为 KeyPrefix + ":changes"，共享前缀的 Hook 自动共用渠道。
func New(client Client, cfg Config) (*Hook, error) {
	if client == nil || cfg.KeyPrefix == "" || strings.TrimSpace(cfg.KeyPrefix) != cfg.KeyPrefix || len(cfg.KeyPrefix) > 256 || cfg.Timeout <= 0 || cfg.Database < 0 {
		return nil, fmt.Errorf("strategy hook requires client, valid prefix and positive timeout")
	}
	return &Hook{client: client, prefix: cfg.KeyPrefix, timeout: cfg.Timeout, channel: cfg.KeyPrefix + ":changes", database: cfg.Database}, nil
}

// Execute 同步更新集合；独立超时只使本插件失败，父上下文取消由 Lifecycle 终止处理。
// SADD/SREM 重放幂等，但旧快照在新快照之后重放仍可能覆盖成员状态。
// 发布失败或响应丢失可能发生在集合已经修改之后；返回错误不表示回滚。
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
	var operation string
	switch input.Alert.Status {
	case domain.AlertStatusActive:
		operation = "SADD"
	case domain.AlertStatusRecovered, domain.AlertStatusClosed:
		operation = "SREM"
	default:
		return result, fmt.Errorf("invalid alert status for strategy hook")
	}
	payload, marshalErr := json.Marshal(changeNotice{Version: 1, BKTenantID: input.Alert.BKTenantID, StrategyID: strategy, Key: key, Database: h.database})
	if marshalErr != nil || len(payload) > 64<<10 {
		return result, fmt.Errorf("strategy hook change notice exceeds encoding limits")
	}
	err = h.client.Eval(call, changeScript, []string{key}, operation, input.Alert.Fingerprint, h.channel, string(payload)).Err()
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
	scalar, exists := labels["strategy_id"]
	if !exists {
		return "", true, nil
	}
	if value, ok := scalar.StringValue(); ok {
		return value, value == "", nil
	}
	if value, ok := scalar.NumberValue(); ok && scalar.Valid() {
		return strconv.FormatFloat(value, 'f', -1, 64), false, nil
	}
	return "", false, fmt.Errorf("strategy_id must be a string or finite number")
}

func invocationID(input lifecycle.FinalHookInput) string {
	// 固定字段数组避免拼接分隔符歧义，身份不依赖当前时间或进程内顺序。
	data, _ := json.Marshal([]string{"linkd:strategy-hook", input.Alert.BKTenantID, input.Alert.AlertID, input.Alert.UpdateAt.UTC().Format(time.RFC3339Nano), input.Cause.Type, input.Cause.ID, string(input.Alert.Status)})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
