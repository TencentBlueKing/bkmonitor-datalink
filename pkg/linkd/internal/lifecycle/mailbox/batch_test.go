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
	"testing"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
)

type batchProbeRedis struct {
	*fakeRedis
	calls    int
	maxItems int
	loseAt   int
}

func (c *batchProbeRedis) Eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd {
	c.calls++
	c.maxItems = max(c.maxItems, len(keys)-1)
	r := c.fakeRedis.Eval(ctx, script, keys, args...)
	if c.calls == c.loseAt {
		return redis.NewCmdResult(nil, errors.New("response lost"))
	}
	return r
}

func TestEnqueueBatchBoundsAndUnknownResult(t *testing.T) {
	for _, loseAt := range []int{0, 2} {
		t.Run(fmt.Sprint(loseAt), func(t *testing.T) {
			client := &batchProbeRedis{fakeRedis: newFakeRedis(), loseAt: loseAt}
			s, _ := newStore(client, Config{KeyPrefix: "test", SignalStream: "signals", MaxPendingPerMailbox: 1000})
			events := make([]domain.Event, 257)
			for i := range events {
				events[i] = mailboxEvent(fmt.Sprint(i))
			}
			results, err := s.EnqueueBatch(t.Context(), events)
			if err != nil {
				t.Fatal(err)
			}
			want := 3
			if loseAt != 0 {
				want = 2
			}
			if client.calls != want || client.maxItems != 128 {
				t.Fatalf("calls=%d max=%d", client.calls, client.maxItems)
			}
			for i, r := range results {
				if (r.Err != nil) != (loseAt != 0 && i >= 128) {
					t.Fatalf("item %d error=%v", i, r.Err)
				}
			}
		})
	}
}

func TestEnqueueBatchFailureStopsPrefix(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprint(invalid), func(t *testing.T) {
			client := &batchProbeRedis{fakeRedis: newFakeRedis()}
			s, _ := newStore(client, Config{KeyPrefix: "test", SignalStream: "signals", MaxPendingPerMailbox: 1})
			events := []domain.Event{mailboxEvent("first"), mailboxEvent("second"), mailboxEvent("third")}
			for i := range events {
				events[i].Fingerprint = fmt.Sprint(i)
			}
			if invalid {
				events[1].EventID = ""
			} else {
				client.lists[s.eventsKey(CorrelationKey(events[1].BKTenantID, events[1].EventSourceID, events[1].Fingerprint))] = []string{"existing"}
			}
			results, err := s.EnqueueBatch(t.Context(), events)
			if err != nil || results[0].Err != nil || results[1].Err == nil || results[2].Err == nil {
				t.Fatalf("results=%v error=%v", results, err)
			}
			if len(client.lists[s.eventsKey(results[2].MailboxID)]) != 0 || client.calls != 1 {
				t.Fatal("executed after failed prefix")
			}
		})
	}
}

func TestEnqueueBatchByteLimitAndCancellation(t *testing.T) {
	client := &batchProbeRedis{fakeRedis: newFakeRedis()}
	s, _ := newStore(client, Config{KeyPrefix: "test", SignalStream: "signals", MaxPendingPerMailbox: 10})
	event := mailboxEvent("event")
	event.Fingerprint = strings.Repeat("f", 600000)
	results, err := s.EnqueueBatch(t.Context(), []domain.Event{event, event})
	if err != nil || results[0].Err != nil || results[1].Err != nil || client.calls != 2 {
		t.Fatal("byte boundary not split")
	}
	event.Fingerprint = strings.Repeat("f", maxEnqueueBytes)
	results, _ = s.EnqueueBatch(t.Context(), []domain.Event{event})
	if results[0].Err == nil || client.calls != 2 {
		t.Fatal("oversized item sent")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	results, _ = s.EnqueueBatch(ctx, []domain.Event{mailboxEvent("cancelled")})
	if !errors.Is(results[0].Err, context.Canceled) || client.calls != 2 {
		t.Fatal("cancelled operation sent")
	}
}
