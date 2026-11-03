// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openalerts

import (
	"context"
	"errors"
	"sync"
)

// A global business strategy runs once and files each event under the
// business the event's target belongs to, not under the strategy's own; the
// alert the link opens carries that business as its label. A close is filed
// under a business too, and the business it must carry is the alert's, which
// for such a strategy no Plan, catalog entry or departure memory knows: the
// strategy's business is the template's. The only place it is written down
// is the alert's own record.
//
// AlertBusinesses reads it there, once per alert. An alert instance keeps
// the business it was opened under for its whole life, so what was read is
// remembered and a close sent again next round reads nothing.

// MaxAlertBusinesses bounds the memory. Past it the memory is dropped whole
// and filled again from the records: a re-read costs one request per alert,
// a memory without a bound costs the process.
const MaxAlertBusinesses = 100000

// ErrAlertBusinessUnknown is a record that does not name the alert's
// business: read, but of another source or strategy, or without one. The
// alert is not closed on a guess.
var ErrAlertBusinessUnknown = errors.New("alarmd openalerts: alert record names no business for this strategy")

// AlertRecordReader reads one alert's record; HTTPReconciler.AlertRecord is
// one.
type AlertRecordReader func(ctx context.Context, tenantID, alertID string) (AlertRecord, error)

// AlertBusinesses answers which business an open alert was filed under.
type AlertBusinesses struct {
	read  AlertRecordReader
	limit int

	mu    sync.Mutex
	known map[string]int64
	// dropped counts the times the memory reached its bound and was
	// dropped, so a deployment that churns through more alerts than it
	// holds is a reading rather than a silence.
	dropped uint64
}

// NewAlertBusinesses builds the resolver over read.
func NewAlertBusinesses(read AlertRecordReader) *AlertBusinesses {
	return &AlertBusinesses{read: read, limit: MaxAlertBusinesses, known: make(map[string]int64)}
}

// Of is the business alert alertID was filed under, read from its record
// unless already known. The record is taken only if it is source's alert of
// strategyID and names a business; otherwise the answer is
// ErrAlertBusinessUnknown. A read that failed is that error, unchanged.
func (b *AlertBusinesses) Of(ctx context.Context, tenantID, source, strategyID, alertID string) (int64, error) {
	key := tenantID + "\x00" + alertID
	b.mu.Lock()
	business, ok := b.known[key]
	b.mu.Unlock()
	if ok {
		return business, nil
	}
	record, err := b.read(ctx, tenantID, alertID)
	if err != nil {
		return 0, err
	}
	if record.EventSourceID != source || record.StrategyID != strategyID || record.BusinessID == 0 {
		return 0, ErrAlertBusinessUnknown
	}
	b.mu.Lock()
	if len(b.known) >= b.limit {
		b.known = make(map[string]int64)
		b.dropped++
	}
	b.known[key] = record.BusinessID
	b.mu.Unlock()
	return record.BusinessID, nil
}

// Dropped is how many times the memory reached its bound.
func (b *AlertBusinesses) Dropped() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}
