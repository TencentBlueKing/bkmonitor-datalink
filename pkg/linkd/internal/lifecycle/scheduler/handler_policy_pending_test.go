// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"fmt"
	"testing"

	"linkd/internal/consume"
	"linkd/internal/processingerror"
	"linkd/internal/store"
)

type pendingPolicyPreparation struct{}

func (pendingPolicyPreparation) Error() string { return "dependency policy initialization in progress" }

func (pendingPolicyPreparation) PolicyPreparationPending() bool { return true }

func TestPolicyPreparationDefersMailboxHeadUntilIndexIsReady(t *testing.T) {
	mailbox := &fakeMailbox{ids: []string{"event-1", "event-2"}}
	processor := &fakeProcessor{err: fmt.Errorf("prepare dependency policy: %w", processingerror.RetryableError{Err: pendingPolicyPreparation{}})}
	handler := newMailboxHandler(t, mailbox, processor, &fakeLocker{}, 128)
	reader := &diagnosticReader{fakeEventReader: handler.eventReader.(*fakeEventReader), saved: map[string]store.EventResult{}}
	handler.eventReader = reader
	for attempt := range 4 {
		out := handler.Handle(t.Context(), signalMessage(t, testEvent("event-1")))
		if out.Kind != consume.OutcomeDefer || out.RetryAfter != DefaultConfig().LockRetryDelay {
			t.Fatalf("pending preparation was not deferred: %+v", out)
		}
		if len(mailbox.ids) != 2 || mailbox.ids[0] != "event-1" || len(processor.ids) != attempt+1 {
			t.Fatal("pending head was retried, acknowledged, or bypassed", mailbox.ids, processor.ids)
		}
		if len(reader.saved) != 0 {
			t.Fatal("pending preparation persisted a rejected Event result")
		}
	}
	processor.err = nil
	out := handler.Handle(t.Context(), signalMessage(t, testEvent("event-1")))
	if out.Kind != consume.OutcomeComplete || len(mailbox.ids) != 0 {
		t.Fatal("ready index did not resume original mailbox", out, mailbox.ids)
	}
	if len(processor.ids) != 6 || processor.ids[4] != "event-1" || processor.ids[5] != "event-2" {
		t.Fatal("ready processing changed queue order", processor.ids)
	}
}
