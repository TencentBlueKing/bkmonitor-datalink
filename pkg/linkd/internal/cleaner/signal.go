// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cleaner

import (
	"context"
	"fmt"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
	"linkd/internal/lifecycle/mailbox"
)

// RedisMailboxPublisher 把已经持久化且仍需处理的 Event ID 加入 fingerprint Mailbox。
type RedisMailboxPublisher struct{ store *mailbox.Store }

func NewRedisMailboxPublisher(client redis.UniversalClient, config mailbox.Config) (*RedisMailboxPublisher, error) {
	store, err := mailbox.NewStore(client, config)
	if err != nil {
		return nil, err
	}
	return &RedisMailboxPublisher{store: store}, nil
}

func (p *RedisMailboxPublisher) EnqueueBatch(ctx context.Context, events []domain.Event) ([]MailboxEnqueueResult, error) {
	results, err := p.store.EnqueueBatch(ctx, events)
	if err != nil {
		return nil, err
	}
	mapped := make([]MailboxEnqueueResult, len(results))
	for index, result := range results {
		mapped[index] = MailboxEnqueueResult{Signaled: result.Signaled, Err: result.Err}
	}
	return mapped, nil
}

// Publish 保留单 Event 调用边界，并委托 Mailbox 入队。
func (p *RedisMailboxPublisher) Publish(ctx context.Context, event domain.Event) error {
	results, err := p.EnqueueBatch(ctx, []domain.Event{event})
	if err != nil {
		return err
	}
	if len(results) != 1 {
		return fmt.Errorf("mailbox publisher returned %d results, want 1", len(results))
	}
	return results[0].Err
}

var _ MailboxWriter = (*RedisMailboxPublisher)(nil)
