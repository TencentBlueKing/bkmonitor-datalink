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
	"strconv"
	"sync"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
)

type fakeRedis struct {
	mu      sync.Mutex
	lists   map[string][]string
	signals int
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{lists: map[string][]string{}}
}

func (f *fakeRedis) Eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	value := int64(0)
	switch script {
	case enqueueScript:
		eventID := args[0].(string)
		perLimit, _ := strconv.Atoi(fmtString(args[1]))
		if len(f.lists[keys[0]]) >= perLimit {
			value = -1
		} else {
			wasEmpty := len(f.lists[keys[0]]) == 0
			f.lists[keys[0]] = append(f.lists[keys[0]], eventID)
			if wasEmpty {
				f.signals++
				value = 2
			} else {
				value = 1
			}
		}
	case ackHeadScript:
		eventID := args[0].(string)
		if len(f.lists[keys[0]]) == 0 {
			value = 0
		} else if f.lists[keys[0]][0] != eventID {
			value = -1
		} else {
			f.lists[keys[0]] = f.lists[keys[0]][1:]
			if len(f.lists[keys[0]]) == 0 {
				delete(f.lists, keys[0])
			}
			value = 1
		}
	}
	cmd := redis.NewCmd(ctx)
	cmd.SetVal(value)
	return cmd
}

func (f *fakeRedis) LIndex(ctx context.Context, key string, index int64) *redis.StringCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	if index != 0 || len(f.lists[key]) == 0 {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(f.lists[key][0], nil)
}

func fmtString(value any) string { return strconv.FormatInt(int64(value.(int)), 10) }

func TestStoreEnqueueAllowsDuplicatesAndSignalsOnlyOnEmptyTransition(t *testing.T) {
	client := newFakeRedis()
	store, err := newStore(client, Config{KeyPrefix: "mailbox", SignalStream: "signals", MaxPendingPerMailbox: 10})
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return time.Unix(100, 0) }
	events := []domain.Event{mailboxEvent("event-1"), mailboxEvent("event-2"), mailboxEvent("event-1")}
	results, err := store.EnqueueBatch(context.Background(), events)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].Signaled || results[1].Signaled || results[2].Signaled || client.signals != 1 {
		t.Fatalf("results=%#v signals=%d", results, client.signals)
	}
	id := results[0].MailboxID
	for _, eventID := range []string{"event-1", "event-2", "event-1"} {
		head, _ := store.Peek(context.Background(), id)
		if head != eventID {
			t.Fatalf("head=%q want=%q", head, eventID)
		}
		if err := store.AckHead(context.Background(), id, eventID); err != nil {
			t.Fatal(err)
		}
	}
	if _, exists := client.lists[store.eventsKey(id)]; exists {
		t.Fatalf("empty list key was not removed: %#v", client.lists)
	}
	results, err = store.EnqueueBatch(context.Background(), []domain.Event{mailboxEvent("event-3")})
	if err != nil || results[0].Err != nil || !results[0].Signaled || client.signals != 2 {
		t.Fatalf("second empty transition results=%#v signals=%d err=%v", results, client.signals, err)
	}
}

func TestStoreRejectsMailboxCapacity(t *testing.T) {
	client := newFakeRedis()
	store, _ := newStore(client, Config{KeyPrefix: "mailbox", SignalStream: "signals", MaxPendingPerMailbox: 128})
	events := make([]domain.Event, 129)
	for index := range events {
		events[index] = mailboxEvent("event-" + strconv.Itoa(index))
	}
	results, _ := store.EnqueueBatch(context.Background(), events)
	for index := range 128 {
		if results[index].Err != nil {
			t.Fatalf("results[%d].Err=%v", index, results[index].Err)
		}
	}
	if results[128].Err == nil {
		t.Fatalf("results=%#v", results)
	}
}

func TestStoreRejectsEmptyEventID(t *testing.T) {
	client := newFakeRedis()
	store, err := newStore(client, Config{KeyPrefix: "mailbox", SignalStream: "signals", MaxPendingPerMailbox: 128})
	if err != nil {
		t.Fatal(err)
	}
	results, err := store.EnqueueBatch(context.Background(), []domain.Event{mailboxEvent("")})
	if err != nil || len(results) != 1 || results[0].Err == nil || client.signals != 0 || len(client.lists) != 0 {
		t.Fatalf("results=%#v signals=%d lists=%#v err=%v", results, client.signals, client.lists, err)
	}
}

func TestStoreDoesNotAckUnexpectedHead(t *testing.T) {
	client := newFakeRedis()
	store, err := newStore(client, Config{KeyPrefix: "mailbox", SignalStream: "signals", MaxPendingPerMailbox: 2})
	if err != nil {
		t.Fatal(err)
	}
	results, _ := store.EnqueueBatch(context.Background(), []domain.Event{mailboxEvent("event-1")})
	if results[0].Err != nil {
		t.Fatal(results[0].Err)
	}
	if err := store.AckHead(context.Background(), results[0].MailboxID, "event-2"); err == nil {
		t.Fatal("AckHead() error = nil")
	}
	head, err := store.Peek(context.Background(), results[0].MailboxID)
	if err != nil || head != "event-1" {
		t.Fatalf("head=%q err=%v", head, err)
	}
}

func mailboxEvent(id string) domain.Event {
	return domain.Event{BKTenantID: "tenant-1", EventSourceID: "source-1", Fingerprint: "fp-1", EventID: id}
}

var _ redisClient = (*fakeRedis)(nil)
