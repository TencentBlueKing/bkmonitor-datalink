// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
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
local results = {}
for i = 2, #KEYS do
  local offset = (i - 2) * 4 + 2
  local count = redis.pcall('LLEN', KEYS[i])
  if type(count) == 'table' and count.err then
    results[#results + 1] = -2
    return results
  end
  if count >= tonumber(ARGV[1]) then
    results[#results + 1] = -1
    return results
  end
  if count == 0 then
    local signal = redis.pcall('XADD', KEYS[1], '*',
      'message_id', ARGV[offset + 1], 'bk_tenant_id', ARGV[offset + 2],
      'order_key', ARGV[offset + 1], 'payload', ARGV[offset + 3])
    if type(signal) == 'table' and signal.err then
      results[#results + 1] = -2
      return results
    end
  end
  local pushed = redis.pcall('RPUSH', KEYS[i], ARGV[offset])
  if type(pushed) == 'table' and pushed.err then
    results[#results + 1] = -2
    return results
  end
  if count == 0 then results[#results + 1] = 2
  else results[#results + 1] = 1 end
end
return results
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

// EnqueueBatch 有界合批并只推进成功前缀，首个失败之后的项不发送且返回错误。
// 已完成切片保留逐项结果；传输错误使当前切片结果未知，重复引用由终态短路收敛。
// Signal 先于 List 追加，脚本意外失败至多留下空唤醒，不能留下无唤醒的 Event。
func (s *Store) EnqueueBatch(ctx context.Context, events []domain.Event) ([]EnqueueResult, error) {
	if len(events) == 0 {
		return nil, fmt.Errorf("enqueue mailbox events: batch must not be empty")
	}
	results := make([]EnqueueResult, len(events))
	for i, event := range events {
		results[i] = EnqueueResult{MailboxID: CorrelationKey(event.BKTenantID, event.EventSourceID, event.Fingerprint), Err: errEnqueueNotAttempted}
	}
	for start := 0; start < len(events); {
		if err := ctx.Err(); err != nil {
			results[start].Err = err
			return results, nil
		}
		keys := []string{s.config.SignalStream}
		args := []any{s.config.MaxPendingPerMailbox}
		end, size := start, len(s.config.SignalStream)
		for end < len(events) && end-start < maxEnqueueOperations {
			event := events[end]
			payload, err := EncodeSignal(NewSignal(event.BKTenantID, event.EventSourceID, event.Fingerprint, s.now()))
			if strings.TrimSpace(event.EventID) == "" {
				err = fmt.Errorf("enqueue mailbox event: event id must not be empty")
			}
			key := s.eventsKey(results[end].MailboxID)
			itemBytes := len(key) + len(event.EventID) + len(results[end].MailboxID) + len(event.BKTenantID) + len(payload) + 128
			if err == nil && itemBytes+len(s.config.SignalStream) > maxEnqueueBytes {
				err = fmt.Errorf("mailbox enqueue item exceeds byte limit")
			}
			if err != nil {
				results[end].Err = err
				break
			}
			if end > start && size+itemBytes > maxEnqueueBytes {
				break
			}
			keys = append(keys, key)
			args = append(args, event.EventID, results[end].MailboxID, event.BKTenantID, payload)
			size += itemBytes
			end++
		}
		if end == start {
			return results, nil
		}
		values, err := s.client.Eval(ctx, enqueueScript, keys, args...).Int64Slice()
		if err != nil {
			for i := start; i < end; i++ {
				results[i].Err = fmt.Errorf("mailbox batch result unknown: %w", err)
			}
			return results, nil
		}
		if len(values) == 0 || len(values) > end-start {
			results[start].Err = fmt.Errorf("mailbox batch returned invalid result count")
			return results, nil
		}
		for i, value := range values {
			switch value {
			case 1, 2:
				results[start+i].Err = nil
				results[start+i].Signaled = value == 2
			case -1:
				results[start+i].Err = fmt.Errorf("mailbox pending limit reached")
				return results, nil
			default:
				results[start+i].Err = fmt.Errorf("mailbox batch operation failed")
				return results, nil
			}
		}
		if len(values) != end-start {
			return results, nil
		}
		start = end
		if start < len(events) && !errors.Is(results[start].Err, errEnqueueNotAttempted) {
			return results, nil
		}
	}
	return results, nil
}

const maxEnqueueOperations = 128

const maxEnqueueBytes = 1 << 20

var errEnqueueNotAttempted = errors.New("mailbox enqueue not attempted after earlier failure")

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
