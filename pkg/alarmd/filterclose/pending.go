// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package filterclose

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

// Pending is the items whose close may have been saved without its
// outputs: a 5xx other than not_attempted, the link's result_uncertain
// above all. Such an alert may no longer match status=active, so no filter
// finds it again, and the contract says to retry it by its id with the
// very same command (b7e5beddf docs/reference/contracts/alert-close.md,
// "按 filter 批量关闭"). One hash in the runtime Redis, keyed by tenant and
// alert id, so every control leader - the next one after a failover too -
// retries what the last one left.
//
// Items whose close was certainly not saved - 429 busy, 504 not_attempted,
// 409 version_conflict - are not kept here: they are still active, and the
// decision's filter finds them again next round and sends the same command.
// That keeps every retry of them behind the filter, so an alert that left
// the decision's bucket in between is not closed by an id remembered from
// before.
type Pending struct {
	client redis.UniversalClient
	key    string
	max    int64
	cursor uint64
}

// MaxPending bounds the hash. An entry is about 1 KB, measured: a 954-byte
// value (the command 297 bytes, held base64; the bucket's re-check filter
// 328) and a 47-byte field, plus Redis' own per-field overhead - so the
// bound is about 22 MB of the runtime Redis. It is reached only by a link
// answering result_uncertain on tens of thousands of closes; past it an
// item is not kept, counted, and its alert's outputs are left to the link.
// The bound is global, not per strategy: at 10k strategies it is the same.
const MaxPending = 20000

// The bounds one item is retried within: attempts, and age from its first
// failure. Past either it is dropped and counted.
const (
	pendingAttempts = 20
	pendingAge      = 24 * time.Hour
)

func NewPending(client redis.UniversalClient, prefix string, max int64) (*Pending, error) {
	if client == nil || strings.TrimSpace(prefix) == "" || strings.ContainsAny(prefix, "{} \t\r\n") || max <= 0 {
		return nil, errors.New("alarmd filterclose: the pending store's client, prefix and bound are required")
	}
	return &Pending{client: client, key: prefix + ":filterclose:pending:v1", max: max}, nil
}

// pendingItem is one kept item: whom to retry, with which exact command,
// and the filter that must still name the alert before it is retried.
type pendingItem struct {
	TenantID      string          `json:"tenant"`
	EventSourceID string          `json:"source"`
	StrategyID    string          `json:"strategy"`
	AlertID       string          `json:"alert"`
	OperationID   string          `json:"op"`
	Command       []byte          `json:"command"`
	Recheck       json.RawMessage `json:"recheck"`
	FirstAt       int64           `json:"first_at"`
	Attempts      int             `json:"attempts"`
}

func pendingField(tenantID, alertID string) string { return tenantID + "\x00" + alertID }

// errPendingFull is an item not kept: the hash is at its bound.
var errPendingFull = errors.New("alarmd filterclose: the pending store is at its bound")

// Keep adds an item unless one is kept for the alert already: the first
// command stays, the one the link may have saved.
func (pending *Pending) Keep(ctx context.Context, item pendingItem) error {
	size, err := pending.client.HLen(ctx, pending.key).Result()
	if err != nil {
		return err
	}
	if size >= pending.max {
		return errPendingFull
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		return err
	}
	return pending.client.HSetNX(ctx, pending.key, pendingField(item.TenantID, item.AlertID), encoded).Err()
}

// Next is up to n kept items, continuing the walk the last call left; an
// unreadable entry is dropped and counted by the caller as dropped.
func (pending *Pending) Next(ctx context.Context, n int) ([]pendingItem, int, error) {
	fields, cursor, err := pending.client.HScan(ctx, pending.key, pending.cursor, "", int64(n)).Result()
	if err != nil {
		return nil, 0, err
	}
	pending.cursor = cursor
	items := make([]pendingItem, 0, len(fields)/2)
	unreadable := 0
	for i := 0; i+1 < len(fields); i += 2 {
		var item pendingItem
		if json.Unmarshal([]byte(fields[i+1]), &item) != nil || item.AlertID == "" || len(item.Command) == 0 {
			_ = pending.client.HDel(ctx, pending.key, fields[i]).Err()
			unreadable++
			continue
		}
		items = append(items, item)
	}
	return items, unreadable, nil
}

// Done drops an item.
func (pending *Pending) Done(ctx context.Context, item pendingItem) error {
	return pending.client.HDel(ctx, pending.key, pendingField(item.TenantID, item.AlertID)).Err()
}

// Again records one more failed attempt.
func (pending *Pending) Again(ctx context.Context, item pendingItem) error {
	item.Attempts++
	encoded, err := json.Marshal(item)
	if err != nil {
		return err
	}
	return pending.client.HSet(ctx, pending.key, pendingField(item.TenantID, item.AlertID), encoded).Err()
}

// Size is how many items are kept.
func (pending *Pending) Size(ctx context.Context) (int64, error) {
	return pending.client.HLen(ctx, pending.key).Result()
}

func (item pendingItem) expired(now time.Time) bool {
	return item.Attempts >= pendingAttempts || now.Sub(time.Unix(item.FirstAt, 0)) >= pendingAge
}
