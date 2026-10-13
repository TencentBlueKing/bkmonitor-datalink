// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"linkd/internal/consume"
	"linkd/internal/lifecycle"
	"linkd/internal/processingerror"
	"linkd/internal/store"
)

type failureProcessor func(context.Context, store.StoredEvent) (lifecycle.ProcessResult, error)

func (f failureProcessor) ProcessEvent(c context.Context, s store.StoredEvent) (lifecycle.ProcessResult, error) {
	return f(c, s)
}

type diagnosticReader struct {
	*fakeEventReader
	saved map[string]store.EventResult
}

func (r *diagnosticReader) CompareAndSetEventResult(_ context.Context, tenant, id string, _ store.VersionToken, result store.EventResult) (store.StoredEvent, error) {
	current := r.events[id]
	if current.Event.BKTenantID != tenant {
		return store.StoredEvent{}, errors.New("wrong tenant")
	}
	r.saved[id] = result
	current.Processing = result.Processing()
	r.events[id] = current
	return current, nil
}

func TestFailureRecordedAndNextMailboxEventContinues(t *testing.T) {
	for _, mode := range []string{"logic", "panic", "502"} {
		t.Run(mode, func(t *testing.T) {
			mailbox := &fakeMailbox{ids: []string{"event-1", "event-2"}}
			h := newMailboxHandler(t, mailbox, &fakeProcessor{}, &fakeLocker{}, 128)
			reader := &diagnosticReader{fakeEventReader: h.eventReader.(*fakeEventReader), saved: map[string]store.EventResult{}}
			h.eventReader = reader
			calls := map[string]int{}
			h.processor = failureProcessor(func(_ context.Context, row store.StoredEvent) (lifecycle.ProcessResult, error) {
				calls[row.Event.EventID]++
				if row.Event.EventID == "event-1" {
					switch mode {
					case "panic":
						panic("secret")
					case "502":
						return lifecycle.ProcessResult{}, processingerror.RetryableError{Err: processingerror.HTTPStatus(502)}
					default:
						return lifecycle.ProcessResult{}, errors.New("bad JSON")
					}
				}
				return lifecycle.ProcessResult{EventID: row.Event.EventID}, nil
			})
			h.Handle(t.Context(), signalMessage(t, testEvent("event-1")))
			want := 1
			code := "processing_failed"
			if mode == "502" {
				want = 3
				code = "retry_exhausted"
			}
			if mode == "panic" {
				code = "handler_panic"
			}
			if calls["event-1"] != want || calls["event-2"] != 1 || len(mailbox.ids) != 0 || reader.saved["event-1"].ReasonCode != code {
				t.Fatal(calls, mailbox.ids, reader.saved)
			}
		})
	}
}

type panicQueue struct{ *fakeMailbox }

func (p panicQueue) Peek(context.Context, string) (string, error) { panic("queue adapter bug") }

type cleanupLocker struct {
	fakeLocker
	releases   int
	panicRenew bool
}

func (l *cleanupLocker) Release(context.Context, Lease) error { l.releases++; return nil }

func (l *cleanupLocker) Renew(context.Context, Lease) error {
	if l.panicRenew {
		panic("renew adapter bug")
	}
	return nil
}

func TestSchedulerPanicAlwaysReleasesLeaseAndRetainsSignal(t *testing.T) {
	for _, renew := range []bool{false, true} {
		t.Run(map[bool]string{false: "queue", true: "renew"}[renew], func(t *testing.T) {
			mailbox := &fakeMailbox{ids: []string{"event-1"}}
			h := newMailboxHandler(t, mailbox, &fakeProcessor{}, &fakeLocker{}, 128)
			locker := &cleanupLocker{panicRenew: renew}
			h.locker = locker
			if renew {
				h.config.RenewInterval = time.Millisecond
				h.processor = failureProcessor(func(ctx context.Context, _ store.StoredEvent) (lifecycle.ProcessResult, error) {
					<-ctx.Done()
					return lifecycle.ProcessResult{}, ctx.Err()
				})
			} else {
				h.mailbox = panicQueue{mailbox}
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			result := h.Handle(ctx, signalMessage(t, testEvent("event-1")))
			if result.Kind != consume.OutcomeDefer || locker.releases != 1 || len(mailbox.ids) != 1 {
				t.Fatal(result, locker.releases, mailbox.ids)
			}
		})
	}
}
