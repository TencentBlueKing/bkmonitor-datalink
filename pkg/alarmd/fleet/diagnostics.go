// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/go-redis/redis/v8"
)

const (
	// DiagnosticRecordsPerObject bounds what one object retains. A window is at
	// most thirty minutes and the flow is already rate limited per minute, so
	// this is a backstop against one object filling the store, not the primary
	// limit.
	DiagnosticRecordsPerObject = 512
	// DiagnosticRetention outlives the longest window so a window's output can
	// still be read for a while after it closes -- an investigation does not
	// end at the same second the window does.
	DiagnosticRetention = time.Hour
	// diagnosticBuffer is how many records may wait to be written. Past it
	// records are dropped and counted rather than queued without bound: this
	// path must never apply back pressure to the pipeline it observes.
	diagnosticBuffer = 4096
)

// DiagnosticStore keeps an observation window's output where the window was
// opened, so it can be read back on the page instead of in a log index.
//
// Writes are fire and forget through a bounded buffer. A store that is slow,
// full or unreachable loses diagnostics and says so; it never slows down or
// fails the execution whose facts it is recording.
type DiagnosticStore struct {
	client          redis.Cmdable
	prefix          string
	records         chan diagnosticWrite
	dropped         atomic.Uint64
	written         atomic.Uint64
	failed          atomic.Uint64
	retention       time.Duration
	perObject       int64
	samples         *observability.SeriesSampler
	samplePerObject int64
	sampleWritten   atomic.Uint64
	sampleFailed    atomic.Uint64
}

type diagnosticWrite struct {
	queryGroup string
	record     []byte
	sample     bool
}

// AttachSeriesSampler is startup wiring. Sample payloads retain a reserved
// buffer until this store's existing writer finishes; they cannot fill the
// lifecycle queue or evict its retained completion records.
func (store *DiagnosticStore) AttachSeriesSampler(sampler *observability.SeriesSampler, recordsPerObject int) error {
	if store == nil || sampler == nil || recordsPerObject <= 0 || recordsPerObject > DiagnosticRecordsPerObject {
		return errors.New("alarmd fleet: sampler and bounded storage allocation are required")
	}
	store.samples, store.samplePerObject = sampler, int64(recordsPerObject)
	return nil
}

// NewDiagnosticStore builds the store. A nil client disables it, which is how a
// deployment that has not wired diagnostics behaves: windows still change what
// is logged, they just cannot be read back here.
func NewDiagnosticStore(client redis.Cmdable, prefix string) (*DiagnosticStore, error) {
	if client == nil {
		return nil, nil
	}
	if prefix == "" {
		return nil, errors.New("alarmd fleet: diagnostic key prefix is required")
	}
	return &DiagnosticStore{
		client: client, prefix: prefix,
		records:   make(chan diagnosticWrite, diagnosticBuffer),
		retention: DiagnosticRetention, perObject: DiagnosticRecordsPerObject,
	}, nil
}

// Run drains the buffer until the context ends. It is the only writer, so the
// store needs no lock and one slow write delays only diagnostics.
func (store *DiagnosticStore) Run(ctx context.Context) {
	if store == nil {
		return
	}
	for {
		// Preserve completion-chain service when both queues are ready.
		select {
		case <-ctx.Done():
			return
		case write := <-store.records:
			store.writeOnce(ctx, write)
			continue
		default:
		}
		select {
		case <-ctx.Done():
			return
		case write := <-store.records:
			store.writeOnce(ctx, write)
		case sample := <-store.samples.Records():
			store.writeOnce(ctx, diagnosticWrite{queryGroup: sample.QueryGroup(), record: sample.Bytes(), sample: true})
			sample.Release()
		}
	}
}

func (store *DiagnosticStore) writeOnce(ctx context.Context, write diagnosticWrite) {
	// A short deadline of its own: the pipeline is not waiting on this, but a
	// write that hangs would stall every record behind it.
	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	key := store.key(write.queryGroup)
	perObject := store.perObject
	if write.sample {
		key = store.sampleKey(write.queryGroup)
		perObject = store.samplePerObject
	}
	pipe := store.client.Pipeline()
	pipe.LPush(writeCtx, key, write.record)
	// Trimmed and expired on every write rather than swept: the store then
	// bounds itself without anything having to remember to clean it, including
	// when the process that wrote it is gone.
	pipe.LTrim(writeCtx, key, 0, perObject-1)
	pipe.Expire(writeCtx, key, store.retention)
	if _, err := pipe.Exec(writeCtx); err != nil {
		if write.sample {
			store.sampleFailed.Add(1)
			return
		}
		store.failed.Add(1)
		return
	}
	if write.sample {
		store.sampleWritten.Add(1)
		return
	}
	store.written.Add(1)
}

// Record queues one record. It never blocks: a full buffer drops and counts.
func (store *DiagnosticStore) Record(queryGroup string, record []byte) {
	if store == nil || queryGroup == "" || len(record) == 0 {
		return
	}
	// The caller may reuse its buffer, so the record is copied before it is
	// handed to another goroutine.
	owned := make([]byte, len(record))
	copy(owned, record)
	select {
	case store.records <- diagnosticWrite{queryGroup: queryGroup, record: owned}:
	default:
		store.dropped.Add(1)
	}
}

// DiagnosticHealth is what the page needs to say whether an empty result means
// "nothing happened" or "this was not recorded".
type DiagnosticHealth struct {
	Written       uint64 `json:"written"`
	Dropped       uint64 `json:"dropped"`
	Failed        uint64 `json:"failed"`
	SampleWritten uint64 `json:"sample_written"`
	SampleFailed  uint64 `json:"sample_failed"`
}

func (store *DiagnosticStore) Health() DiagnosticHealth {
	if store == nil {
		return DiagnosticHealth{}
	}
	return DiagnosticHealth{
		Written: store.written.Load(), Dropped: store.dropped.Load(), Failed: store.failed.Load(),
		SampleWritten: store.sampleWritten.Load(), SampleFailed: store.sampleFailed.Load(),
	}
}

// Load returns the most recent records for one object, newest first.
func (store *DiagnosticStore) Load(ctx context.Context, queryGroup string, limit int) ([]json.RawMessage, error) {
	if store == nil {
		return nil, errors.New("alarmd fleet: diagnostics are not wired")
	}
	if queryGroup == "" {
		return nil, errors.New("alarmd fleet: query group is required")
	}
	if limit <= 0 || limit > DiagnosticRecordsPerObject {
		limit = DiagnosticRecordsPerObject
	}
	raw, err := store.client.LRange(ctx, store.key(queryGroup), 0, int64(limit-1)).Result()
	if err != nil {
		return nil, err
	}
	records := make([]json.RawMessage, 0, len(raw))
	for _, line := range raw {
		records = append(records, json.RawMessage(line))
	}
	return records, nil
}

func (store *DiagnosticStore) key(queryGroup string) string {
	return store.prefix + ":diag:v1:" + queryGroup
}

func (store *DiagnosticStore) sampleKey(queryGroup string) string {
	return store.prefix + ":diag:series:v1:" + queryGroup
}

// LoadSeriesSamples is explicitly opt-in and bounded by the assigned retained
// count. WindowID in each record distinguishes closed/reopened generations.
func (store *DiagnosticStore) LoadSeriesSamples(ctx context.Context, queryGroup string, limit int) ([]json.RawMessage, error) {
	if store == nil || store.samples == nil {
		return nil, errors.New("alarmd fleet: series samples are not wired")
	}
	if queryGroup == "" {
		return nil, errors.New("alarmd fleet: query group is required")
	}
	if limit <= 0 || int64(limit) > store.samplePerObject {
		limit = int(store.samplePerObject)
	}
	raw, err := store.client.LRange(ctx, store.sampleKey(queryGroup), 0, int64(limit-1)).Result()
	if err != nil {
		return nil, err
	}
	result := make([]json.RawMessage, 0, len(raw))
	for _, record := range raw {
		result = append(result, json.RawMessage(record))
	}
	return result, nil
}
