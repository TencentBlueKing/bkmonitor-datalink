// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package alertqueue 通过 Redis Streams 发布轻量告警变更，不持有投影或动作执行状态。
package alertqueue

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
)

// Stream 隔离部署；消息中的租户和 Alert ID 决定业务作用域。
func Stream(deployment string) string {
	return "linkd:kac:" + base64.RawURLEncoding.EncodeToString([]byte(deployment))
}

// Publisher 复用进程 Redis 连接，仅发布小消息；失败由业务入口记录，不回滚业务写入。
type Publisher struct {
	Client redis.UniversalClient
	Stream string
}

// Publish 将单次通知写入 Stream，最长等待一秒，不向消息附加完整 Alert。
func (p *Publisher) Publish(ctx context.Context, change domain.AlertChange) error {
	raw, err := json.Marshal(change)
	if err != nil {
		return err
	}
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return p.Client.XAdd(call, &redis.XAddArgs{Stream: p.Stream, Values: map[string]any{"body": string(raw)}}).Err()
}

// PublishBatch 将本批轻量通知放入一次 pipeline，每条消息仍有独立投递结果。
func (p *Publisher) PublishBatch(ctx context.Context, changes []domain.AlertChange) []error {
	results := make([]error, len(changes))
	commands := make([]*redis.StringCmd, len(changes))
	pipe := p.Client.Pipeline()
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	for i, c := range changes {
		raw, err := json.Marshal(c)
		if err != nil {
			results[i] = err
			continue
		}
		commands[i] = pipe.XAdd(call, &redis.XAddArgs{Stream: p.Stream, Values: map[string]any{"body": string(raw)}})
	}
	_, _ = pipe.Exec(call)
	for i, c := range commands {
		if c != nil {
			results[i] = c.Err()
		}
	}
	return results
}
