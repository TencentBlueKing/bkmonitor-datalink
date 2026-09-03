// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"linkd/internal/consume"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/store"
)

type fakeEventReader struct{ events map[string]store.StoredEvent }

func (r *fakeEventReader) GetEvent(_ context.Context, _, eventID string) (store.StoredEvent, error) {
	event, ok := r.events[eventID]
	if !ok {
		return store.StoredEvent{}, store.ErrNotFound
	}
	return event, nil
}

type fakeProcessor struct {
	ids []string
	err error
}

func (p *fakeProcessor) ProcessEvent(_ context.Context, _, eventID string) (lifecycle.ProcessResult, error) {
	p.ids = append(p.ids, eventID)
	if p.err != nil {
		return lifecycle.ProcessResult{}, p.err
	}
	return lifecycle.ProcessResult{EventID: eventID, AlertID: "alert-1", Outcome: lifecycle.OutcomeAlertUpdated}, nil
}

type fakeMailbox struct {
	ids []string
}

func (m *fakeMailbox) Peek(context.Context, string) (string, error) {
	if len(m.ids) == 0 {
		return "", nil
	}
	return m.ids[0], nil
}

func (m *fakeMailbox) AckHead(_ context.Context, _, eventID string) error {
	if len(m.ids) == 0 || m.ids[0] != eventID {
		return errors.New("head mismatch")
	}
	m.ids = m.ids[1:]
	return nil
}

type fakeLocker struct{ acquireErr error }

func (l *fakeLocker) Acquire(context.Context, string) (Lease, error) {
	if l.acquireErr != nil {
		return Lease{}, l.acquireErr
	}
	return Lease{key: "key", token: "token"}, nil
}

func (*fakeLocker) Renew(context.Context, Lease) error { return nil }

func (*fakeLocker) Release(context.Context, Lease) error { return nil }

func TestHandlerDrainsMailboxBeforeComplete(t *testing.T) {
	mailbox := &fakeMailbox{ids: []string{"event-1", "event-2"}}
	processor := &fakeProcessor{}
	handler := newMailboxHandler(t, mailbox, processor, &fakeLocker{}, 512)
	outcome := handler.Handle(context.Background(), signalMessage(t, testEvent("event-1")))
	if outcome.Kind != consume.OutcomeComplete || len(mailbox.ids) != 0 || len(processor.ids) != 2 {
		t.Fatalf("outcome=%#v mailbox=%v processed=%v", outcome, mailbox.ids, processor.ids)
	}
}

func TestHandlerCompletesRedundantSignalForEmptyMailbox(t *testing.T) {
	mailbox := &fakeMailbox{}
	processor := &fakeProcessor{}
	handler := newMailboxHandler(t, mailbox, processor, &fakeLocker{}, 512)
	outcome := handler.Handle(context.Background(), signalMessage(t, testEvent("event-1")))
	if outcome.Kind != consume.OutcomeComplete || len(processor.ids) != 0 {
		t.Fatalf("outcome=%#v processed=%v", outcome, processor.ids)
	}
}

func TestHandlerDefersBusyLeaseWithoutRetry(t *testing.T) {
	handler := newMailboxHandler(t, &fakeMailbox{}, &fakeProcessor{}, &fakeLocker{acquireErr: ErrLockBusy}, 512)
	outcome := handler.Handle(context.Background(), signalMessage(t, testEvent("event-1")))
	if outcome.Kind != consume.OutcomeDefer || outcome.RetryAfter != DefaultConfig().LockRetryDelay {
		t.Fatalf("outcome=%#v", outcome)
	}
}

func TestHandlerYieldsAfterDrainBudget(t *testing.T) {
	mailbox := &fakeMailbox{ids: []string{"event-1", "event-2"}}
	handler := newMailboxHandler(t, mailbox, &fakeProcessor{}, &fakeLocker{}, 1)
	outcome := handler.Handle(context.Background(), signalMessage(t, testEvent("event-1")))
	if outcome.Kind != consume.OutcomeDefer || len(mailbox.ids) != 1 {
		t.Fatalf("outcome=%#v mailbox=%#v", outcome, mailbox)
	}
}

func TestHandlerKeepsHeadUntilEventProcessingSucceeds(t *testing.T) {
	mailbox := &fakeMailbox{ids: []string{"event-1"}}
	temporary := errors.New("temporary lifecycle failure")
	handler := newMailboxHandler(t, mailbox, &fakeProcessor{err: temporary}, &fakeLocker{}, 512)
	outcome := handler.Handle(context.Background(), signalMessage(t, testEvent("event-1")))
	if outcome.Kind != consume.OutcomeRetry || !errors.Is(outcome.Err, temporary) {
		t.Fatalf("outcome=%#v", outcome)
	}
	if len(mailbox.ids) != 1 || mailbox.ids[0] != "event-1" {
		t.Fatalf("mailbox head was removed before success: %v", mailbox.ids)
	}
}

func TestHandlerDiscardsInvalidSignal(t *testing.T) {
	handler := newMailboxHandler(t, &fakeMailbox{}, &fakeProcessor{}, &fakeLocker{}, 1)
	outcome := handler.Handle(context.Background(), consume.Message{ID: "bad", TenantID: "tenant-1", Body: []byte(`{}`)})
	if outcome.Kind != consume.OutcomeDiscard {
		t.Fatalf("outcome=%#v", outcome)
	}
}

func newMailboxHandler(t *testing.T, mailbox *fakeMailbox, processor *fakeProcessor, locker *fakeLocker, maxDrain int) *Handler {
	t.Helper()
	events := map[string]store.StoredEvent{}
	for _, id := range []string{"event-1", "event-2"} {
		event := testEvent(id)
		events[id] = store.StoredEvent{Event: event, Processing: store.NewUnprocessedEventProcessing(), Version: store.NewVersionToken("1")}
	}
	config := DefaultConfig()
	config.MaxDrainEvents = maxDrain
	handler, err := NewHandler(&fakeEventReader{events: events}, mailbox, processor, locker, config,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func signalMessage(t *testing.T, event domain.Event) consume.Message {
	t.Helper()
	signal := NewSignal(event, time.Unix(100, 0))
	body, err := EncodeSignal(signal)
	if err != nil {
		t.Fatal(err)
	}
	return consume.Message{ID: signal.MessageID, TenantID: signal.BKTenantID, OrderKey: signal.MailboxID, Body: body}
}

func testEvent(id string) domain.Event {
	return domain.Event{BKTenantID: "tenant-1", EventSourceID: "source-1", EventID: id, Fingerprint: "fp-1"}
}
