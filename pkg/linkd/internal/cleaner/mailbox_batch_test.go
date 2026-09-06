// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cleaner

import (
	"context"
	"errors"
	"slices"
	"testing"

	"linkd/internal/domain"
)

type prefixMailboxWriter struct {
	calls   [][]string
	failure error
}

func (m *prefixMailboxWriter) EnqueueBatch(_ context.Context, events []domain.Event) ([]MailboxEnqueueResult, error) {
	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.EventID
	}
	m.calls = append(m.calls, ids)
	results := make([]MailboxEnqueueResult, len(events))
	if len(m.calls) == 1 {
		for i := 1; i < len(results); i++ {
			results[i].Err = m.failure
		}
	}
	return results, nil
}

func TestCleanerMailboxBatchPreservesSuccessfulPrefix(t *testing.T) {
	s := newRuntimeTestSession(nil, struct{ lane, id string }{"lane", "a1"}, struct{ lane, id string }{"lane", "a2"}, struct{ lane, id string }{"lane", "a3"})
	entries := make([]*cleanerEntry, len(s.deliveries))
	for i, d := range s.deliveries {
		entries[i] = &cleanerEntry{stored: true, delivery: d, event: domain.Event{EventID: d.Message.ID}}
	}
	w := &prefixMailboxWriter{failure: errors.New("full")}
	r := &Runtime{session: s, mailboxes: w}
	first := r.finishLanePrefix(t.Context(), "lane", entries, nil)
	if first.settled != 1 || !errors.Is(first.err, w.failure) || !slices.Equal(s.confirmed, []string{"a1"}) {
		t.Fatalf("first=%+v confirmations=%v", first, s.confirmed)
	}
	second := r.finishLanePrefix(t.Context(), "lane", entries[1:], nil)
	if second.settled != 2 || second.err != nil || len(w.calls) != 2 || !slices.Equal(w.calls[0], []string{"a1", "a2", "a3"}) || !slices.Equal(w.calls[1], []string{"a2", "a3"}) {
		t.Fatalf("second=%+v calls=%v", second, w.calls)
	}
	if !slices.Equal(s.confirmed, []string{"a1", "a2", "a3"}) {
		t.Fatal(s.confirmed)
	}
}
