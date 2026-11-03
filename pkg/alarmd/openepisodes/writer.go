// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openepisodes

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The writer's outcomes, closed: a metric label is made of them.
const (
	// OutcomeOpened and OutcomeEnded are entries written and removed.
	OutcomeOpened = "opened"
	OutcomeEnded  = "ended"
	// OutcomeRefreshed is a hash whose life was set again without a
	// change: a strategy that only repeats its ABNORMALs.
	OutcomeRefreshed = "refreshed"
	// OutcomeQueueFull is a change not queued because the queue was full:
	// the ACK path never waits on the record. Counted per change.
	OutcomeQueueFull = "queue_full"
	// OutcomeWriteFailed is a change in a pipeline the Redis did not take,
	// counted per change. An ABNORMAL lost so is missing from the record
	// until this process restarts (the copy holds its own send and does not
	// hand it over again): the strategy's next close sees a dry run past
	// its count and refuses, counted - no close on a short record.
	OutcomeWriteFailed = "write_failed"
)

// Outcomes lists every writer outcome.
var Outcomes = []string{OutcomeOpened, OutcomeEnded, OutcomeRefreshed, OutcomeQueueFull, OutcomeWriteFailed}

// QueueBatches bounds the acknowledged batches waiting for the writer.
const QueueBatches = 4096

// writeTimeout bounds one pipeline.
const writeTimeout = 5 * time.Second

type batch struct {
	changes []Change
	refresh []Key
}

// Writer takes acknowledged envelopes on the worker's ACK path, without
// waiting, and writes them to the record from its own goroutine, in the
// order they were acknowledged (one queue, one writer: an ABNORMAL and the
// RECOVERY after it land in that order).
type Writer struct {
	store *Store
	now   func() time.Time
	queue chan batch
	// pending is the batches queued or being written.
	pending atomic.Int64

	mu sync.Mutex
	// refreshed is when each strategy's hash life was last set by this
	// process.
	refreshed map[Key]time.Time
	counts    map[string]uint64
}

func NewWriter(store *Store, now func() time.Time) (*Writer, error) {
	if store == nil {
		return nil, errors.New("alarmd openepisodes: the writer needs the record")
	}
	if now == nil {
		now = time.Now
	}
	return &Writer{store: store, now: now, queue: make(chan batch, QueueBatches), refreshed: map[Key]time.Time{},
		counts: map[string]uint64{}}, nil
}

// Acknowledged queues what one acknowledged batch changes: changed is the
// copy's word on which envelopes open or end an episode
// (activecopy.Copy.Acknowledged), acked the whole batch, whose strategies'
// hashes have their life set again once a RefreshEvery.
func (writer *Writer) Acknowledged(acked, changed []contract.TriggerEventV1) {
	if writer == nil {
		return
	}
	now := writer.now()
	var next batch
	for _, event := range changed {
		if change, ok := ChangeOf(event, now); ok {
			next.changes = append(next.changes, change)
		}
	}
	writer.mu.Lock()
	for _, change := range next.changes {
		writer.refreshed[change.Key] = now
	}
	for _, event := range acked {
		if event.EventKind != contract.TriggerEventAbnormal || event.DedupeMD5 == "" || event.StrategyRef == nil ||
			event.LegacyOutput != nil {
			continue
		}
		key := Key{TenantID: event.TenantID, StrategyID: event.PlanRef.StrategyID}
		if last, ok := writer.refreshed[key]; !ok || now.Sub(last) >= RefreshEvery {
			writer.refreshed[key] = now
			next.refresh = append(next.refresh, key)
		}
	}
	writer.mu.Unlock()
	if len(next.changes) == 0 && len(next.refresh) == 0 {
		return
	}
	writer.pending.Add(1)
	select {
	case writer.queue <- next:
	default:
		writer.pending.Add(-1)
		writer.count(OutcomeQueueFull, len(next.changes))
		writer.forgetRefresh(next)
	}
}

// Run writes the queue until ctx ends.
func (writer *Writer) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case next := <-writer.queue:
			writer.write(ctx, next)
			writer.pending.Add(-1)
		}
	}
}

func (writer *Writer) write(ctx context.Context, next batch) {
	write, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	if err := writer.store.Apply(write, next.changes, next.refresh); err != nil {
		writer.count(OutcomeWriteFailed, len(next.changes))
		writer.forgetRefresh(next)
		return
	}
	opened := 0
	for _, change := range next.changes {
		if change.Open {
			opened++
		}
	}
	writer.count(OutcomeOpened, opened)
	writer.count(OutcomeEnded, len(next.changes)-opened)
	writer.count(OutcomeRefreshed, len(next.refresh))
}

// Pending is the batches queued or being written.
func (writer *Writer) Pending() int64 {
	if writer == nil {
		return 0
	}
	return writer.pending.Load()
}

// forgetRefresh lets the next batch set the lives a lost one did not.
func (writer *Writer) forgetRefresh(lost batch) {
	writer.mu.Lock()
	for _, key := range lost.refresh {
		delete(writer.refreshed, key)
	}
	for _, change := range lost.changes {
		delete(writer.refreshed, change.Key)
	}
	writer.mu.Unlock()
}

func (writer *Writer) count(outcome string, n int) {
	if n <= 0 {
		return
	}
	writer.mu.Lock()
	writer.counts[outcome] += uint64(n)
	writer.mu.Unlock()
}

// Stats is every outcome's count.
func (writer *Writer) Stats() map[string]uint64 {
	counts := make(map[string]uint64, len(Outcomes))
	for _, outcome := range Outcomes {
		counts[outcome] = 0
	}
	if writer == nil {
		return counts
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	for _, outcome := range Outcomes {
		counts[outcome] = writer.counts[outcome]
	}
	return counts
}
