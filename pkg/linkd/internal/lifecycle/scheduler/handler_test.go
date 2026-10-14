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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"linkd/internal/consume"
	"linkd/internal/domain"
	"linkd/internal/enrich/description"
	"linkd/internal/lifecycle"
	"linkd/internal/processingerror"
	"linkd/internal/store"
)

type fakeEventReader struct {
	events map[string]store.StoredEvent
	reads  int
}

func TestContentFailureDiscardsAndDrainsLaterEvents(t *testing.T) {
	for _, failure := range []error{errors.New("logic failed"), &description.Error{Code: "history_value_missing"}, processingerror.RetryableError{Err: processingerror.HTTPStatus(502)}} {
		mailbox := &fakeMailbox{ids: []string{"event-1", "event-2"}}
		processor := &fakeProcessor{err: failure}
		h := newMailboxHandler(t, mailbox, processor, &fakeLocker{}, 512)
		out := h.Handle(t.Context(), signalMessage(t, testEvent("event-1")))
		expected := 2
		if processingerror.Retryable(failure) {
			expected = 6
		}
		if out.Kind != consume.OutcomeComplete || len(mailbox.ids) != 0 || len(processor.ids) != expected {
			t.Fatal(out, mailbox.ids, processor.ids)
		}
	}
}

func TestHandlerLogsDiscardCause(t *testing.T) {
	contentErr := fmt.Errorf("build alert content: resolve description facts: %w", &description.Error{Code: "nodata_evidence_missing"})
	for _, tc := range []struct {
		name     string
		failure  error
		stage    string
		reason   string
		attempts float64
	}{
		{name: "content failure", failure: contentErr, stage: "event_process", reason: "processing_failed", attempts: 1},
		{name: "read failure", failure: store.ErrNotFound, stage: "event_read", reason: "processing_failed", attempts: 1},
		{name: "identity mismatch", failure: errors.New("mailbox event identity mismatch"), stage: "event_identity", reason: "processing_failed", attempts: 1},
		{name: "retry exhausted", failure: processingerror.RetryableError{Err: processingerror.HTTPStatus(502)}, stage: "event_process", reason: "retry_exhausted", attempts: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mailbox := &fakeMailbox{ids: []string{"event-1"}}
			h := newMailboxHandler(t, mailbox, &fakeProcessor{err: tc.failure}, &fakeLocker{}, 512)
			reader := h.eventReader.(*fakeEventReader)
			stored := reader.events["event-1"]
			stored.Event.Content = "private alert content"
			if tc.stage == "event_identity" {
				stored.Event.Fingerprint = "other"
			}
			reader.events["event-1"] = stored
			if tc.stage == "event_read" {
				delete(reader.events, "event-1")
			}
			var logs bytes.Buffer
			h.logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
			out := h.Handle(t.Context(), signalMessage(t, testEvent("event-1")))
			if out.Kind != consume.OutcomeComplete || len(mailbox.ids) != 0 {
				t.Fatalf("outcome=%#v mailbox=%v", out, mailbox.ids)
			}
			var entry map[string]any
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			for field, expected := range map[string]any{
				"msg": "discard lifecycle event", "module": "lifecycle", "operation": tc.stage, "error": tc.failure.Error(), "stage": tc.stage,
				"reason_code": tc.reason, "attempts": tc.attempts, "bk_tenant_id": "tenant-1",
				"event_source_id": "source-1", "event_id": "event-1",
				"mailbox_id": NewSignal(testEvent("event-1"), time.Unix(100, 0)).MailboxID,
			} {
				if entry[field] != expected {
					t.Errorf("log field %s=%v, want %v", field, entry[field], expected)
				}
			}
			if strings.Contains(logs.String(), stored.Event.Content) {
				t.Fatal("discard log contains alert content")
			}
		})
	}
}

func (r *fakeEventReader) GetEvent(_ context.Context, _, eventID string) (store.StoredEvent, error) {
	r.reads++
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

func (p *fakeProcessor) ProcessEvent(_ context.Context, stored store.StoredEvent) (lifecycle.ProcessResult, error) {
	eventID := stored.Event.EventID
	p.ids = append(p.ids, eventID)
	if p.err != nil {
		return lifecycle.ProcessResult{}, p.err
	}
	return lifecycle.ProcessResult{EventID: eventID, AlertIDs: []string{"alert-1"}, Outcome: lifecycle.OutcomeAlertUpdated}, nil
}

type fakeMailbox struct {
	ids    []string
	ackErr error
}

func (m *fakeMailbox) Peek(context.Context, string) (string, error) {
	if len(m.ids) == 0 {
		return "", nil
	}
	return m.ids[0], nil
}

func (m *fakeMailbox) AckHead(_ context.Context, _, eventID string) error {
	if m.ackErr != nil {
		return m.ackErr
	}
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
	reader := handler.eventReader.(*fakeEventReader)
	outcome := handler.Handle(context.Background(), signalMessage(t, testEvent("event-1")))
	if outcome.Kind != consume.OutcomeComplete || len(mailbox.ids) != 0 || len(processor.ids) != 2 || reader.reads != 2 {
		t.Fatalf("outcome=%#v mailbox=%v processed=%v reads=%d", outcome, mailbox.ids, processor.ids, reader.reads)
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

func TestHandlerOrdinaryFailureDoesNotKeepHead(t *testing.T) {
	mailbox := &fakeMailbox{ids: []string{"event-1"}}
	processor := &fakeProcessor{err: errors.New("logic failure")}
	h := newMailboxHandler(t, mailbox, processor, &fakeLocker{}, 512)
	out := h.Handle(t.Context(), signalMessage(t, testEvent("event-1")))
	if out.Kind != consume.OutcomeComplete || len(mailbox.ids) != 0 || len(processor.ids) != 1 {
		t.Fatal(out, mailbox.ids, processor.ids)
	}
}

func TestHandlerKeepsHeadUntilAckSucceeds(t *testing.T) {
	ackErr := errors.New("temporary ack failure")
	mailbox := &fakeMailbox{ids: []string{"event-1"}, ackErr: ackErr}
	processor := &fakeProcessor{}
	handler := newMailboxHandler(t, mailbox, processor, &fakeLocker{}, 512)
	outcome := handler.Handle(context.Background(), signalMessage(t, testEvent("event-1")))
	if outcome.Kind != consume.OutcomeDefer {
		t.Fatalf("outcome=%#v", outcome)
	}
	if len(mailbox.ids) != 1 || len(processor.ids) != 1 {
		t.Fatalf("mailbox=%v processed=%v", mailbox.ids, processor.ids)
	}
}

func TestHandlerBlocksMismatchedMailboxIdentityBeforeProcessing(t *testing.T) {
	mailbox := &fakeMailbox{ids: []string{"event-1"}}
	processor := &fakeProcessor{}
	handler := newMailboxHandler(t, mailbox, processor, &fakeLocker{}, 512)
	reader := handler.eventReader.(*fakeEventReader)
	stored := reader.events["event-1"]
	stored.Event.Fingerprint = "other"
	reader.events["event-1"] = stored

	outcome := handler.Handle(context.Background(), signalMessage(t, testEvent("event-1")))
	if outcome.Kind != consume.OutcomeComplete || len(processor.ids) != 0 || len(mailbox.ids) != 0 || reader.reads != 1 {
		t.Fatalf(
			"outcome=%#v processed=%v mailbox=%v reads=%d",
			outcome,
			processor.ids,
			mailbox.ids,
			reader.reads,
		)
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
	return domain.Event{EventSourceVersion: 1, BKTenantID: "tenant-1", EventSourceID: "source-1", EventID: id, Fingerprint: "fp-1"}
}
