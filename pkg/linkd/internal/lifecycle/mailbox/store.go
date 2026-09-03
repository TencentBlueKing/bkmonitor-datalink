// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mailbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
)

const enqueueScript = `
local mailbox_count = redis.call('LLEN', KEYS[1])
if mailbox_count >= tonumber(ARGV[2]) then return -1 end
if mailbox_count == 0 then
  redis.call('XADD', KEYS[2], '*',
    'message_id', ARGV[3], 'bk_tenant_id', ARGV[4], 'order_key', ARGV[3], 'payload', ARGV[5])
end
redis.call('RPUSH', KEYS[1], ARGV[1])
if mailbox_count == 0 then return 2 end
return 1
`

const ackHeadScript = `
local head = redis.call('LINDEX', KEYS[1], 0)
if not head then return 0 end
if head ~= ARGV[1] then return -1 end
redis.call('LPOP', KEYS[1])
return 1
`

type redisClient interface {
	Eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd
	LIndex(ctx context.Context, key string, index int64) *redis.StringCmd
}

type Config struct {
	KeyPrefix            string
	SignalStream         string
	MaxPendingPerMailbox int
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.KeyPrefix) == "" || strings.TrimSpace(c.SignalStream) == "" {
		return fmt.Errorf("mailbox key prefix and signal stream are required")
	}
	if c.MaxPendingPerMailbox < 1 {
		return fmt.Errorf("mailbox limits are invalid")
	}
	return nil
}

type EnqueueResult struct {
	MailboxID string
	Signaled  bool
	Err       error
}

// Store 使用单一 Redis List 保存 Event 引用，并在 Mailbox 从空变为非空时写入 Stream Signal。
type Store struct {
	client redisClient
	config Config
	now    func() time.Time
}

func NewStore(client redis.UniversalClient, config Config) (*Store, error) {
	if client == nil {
		return nil, fmt.Errorf("create mailbox store: redis client must not be nil")
	}
	return newStore(client, config)
}

func newStore(client redisClient, config Config) (*Store, error) {
	if client == nil {
		return nil, fmt.Errorf("create mailbox store: redis client must not be nil")
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("create mailbox store: %w", err)
	}
	return &Store{client: client, config: config, now: time.Now}, nil
}

// EnqueueBatch 按输入顺序加入 Event ID；重复引用由 Lifecycle 的终态短路保证幂等。
// List 追加和空到非空时的 Signal 写入位于同一脚本内，任一成功入队都不会丢失唤醒。
func (s *Store) EnqueueBatch(ctx context.Context, events []domain.Event) ([]EnqueueResult, error) {
	if len(events) == 0 {
		return nil, fmt.Errorf("enqueue mailbox events: batch must not be empty")
	}
	results := make([]EnqueueResult, len(events))
	for index, event := range events {
		result := EnqueueResult{MailboxID: CorrelationKey(event.BKTenantID, event.EventSourceID, event.Fingerprint)}
		if strings.TrimSpace(event.EventID) == "" {
			result.Err = fmt.Errorf("enqueue mailbox event: event id must not be empty")
			results[index] = result
			continue
		}
		signal := NewSignal(event.BKTenantID, event.EventSourceID, event.Fingerprint, s.now())
		payload, err := EncodeSignal(signal)
		if err != nil {
			result.Err = err
			results[index] = result
			continue
		}
		value, err := s.client.Eval(ctx, enqueueScript, []string{s.eventsKey(result.MailboxID), s.config.SignalStream},
			event.EventID, s.config.MaxPendingPerMailbox, result.MailboxID, event.BKTenantID, payload,
		).Int64()
		if err != nil {
			result.Err = fmt.Errorf("enqueue event %q to mailbox: %w", event.EventID, err)
		} else {
			switch value {
			case -1:
				result.Err = fmt.Errorf("mailbox %q pending limit reached", result.MailboxID)
			case 1:
			case 2:
				result.Signaled = true
			default:
				result.Err = fmt.Errorf("mailbox enqueue returned unexpected result %d", value)
			}
		}
		results[index] = result
	}
	return results, nil
}

func (s *Store) Peek(ctx context.Context, mailboxID string) (string, error) {
	if strings.TrimSpace(mailboxID) == "" {
		return "", fmt.Errorf("peek mailbox: mailbox id must not be empty")
	}
	value, err := s.client.LIndex(ctx, s.eventsKey(mailboxID), 0).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("peek mailbox %q: %w", mailboxID, err)
	}
	return value, nil
}

func (s *Store) AckHead(ctx context.Context, mailboxID, eventID string) error {
	if strings.TrimSpace(mailboxID) == "" || strings.TrimSpace(eventID) == "" {
		return fmt.Errorf("ack mailbox head: mailbox id and event id must not be empty")
	}
	value, err := s.client.Eval(ctx, ackHeadScript, []string{s.eventsKey(mailboxID)}, eventID).Int64()
	if err != nil {
		return fmt.Errorf("ack mailbox %q head: %w", mailboxID, err)
	}
	if value != 1 {
		return fmt.Errorf("ack mailbox %q head %q returned %d", mailboxID, eventID, value)
	}
	return nil
}

func (s *Store) eventsKey(id string) string { return s.config.KeyPrefix + ":" + id + ":events" }
