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
	"strings"

	"github.com/redis/go-redis/v9"
)

// ErrAuthorityLost 表示控制面或任务已失去 taskdispatch 执行授权，不能继续修改接收状态。
var ErrAuthorityLost = errors.New("collector execution authority lost")

// WithLeadership 返回复用已有 Redis 领导权的存储实例，不创建或续租任何锁。
// 必须将控制面实际持有的 key/token 一起传入，不能从业务请求取得。
func (r *RedisReceipts) WithLeadership(key, token string) *RedisReceipts {
	copy := *r
	copy.authorityKey, copy.authorityToken = key, token
	return &copy
}

// AuthorizedWriter 在执行实际业务脚本的同一次 Redis 调用中核对已有授权。
// 实现必须保留业务脚本 KEYS/ARGV 的既有下标，不能先检查授权再单独写入。
type AuthorizedWriter interface {
	Eval(context.Context, string, []string, ...any) *redis.Cmd
}

// WithWriter 返回绑定任务授权写入器的存储副本，用于独立拉取 worker。
func (r *RedisReceipts) WithWriter(writer AuthorizedWriter) *RedisReceipts {
	copy := *r
	copy.writer = writer
	return &copy
}

func (r *RedisReceipts) eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd {
	if r.authorityKey != "" {
		// 检查和业务写入不可拆成两次 Redis 调用，否则检查通过后仍可能发生领导权交接。
		script = `if redis.call('GET', KEYS[#KEYS]) ~= ARGV[#ARGV] then return redis.error_reply('COLLECTOR_AUTHORITY_LOST') end
` + script
		keys = append(keys, r.authorityKey)
		args = append(args, r.authorityToken)
	}
	writer := r.writer
	if writer == nil {
		writer = r.client
	}
	command := writer.Eval(ctx, script, keys, args...)
	if err := command.Err(); err != nil {
		switch strings.TrimPrefix(err.Error(), "ERR ") {
		case "COLLECTOR_AUTHORITY_LOST", "TASK_AUTHORITY_LOST":
			command.SetErr(ErrAuthorityLost)
		}
	}
	return command
}
