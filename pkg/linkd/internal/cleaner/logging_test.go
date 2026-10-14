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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/domain"
	"linkd/internal/logging"
	"linkd/internal/store"
)

type invalidLoggedProcessor struct{}

func (invalidLoggedProcessor) Process(_ context.Context, message consume.Message) (ProcessResult, error) {
	if message.ID == "bad" {
		return ProcessResult{DiscardErr: errors.New("decode standard payload: unexpected EOF")}, nil
	}
	return runtimeTestProcessor{}.Process(context.Background(), message)
}

func TestInvalidRawMessageLogsCauseAndContinues(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	session := newRuntimeTestSession(cancel, struct{ lane, id string }{"topic:0", "bad"}, struct{ lane, id string }{"topic:0", "good"})
	session.cancelAfter = 2
	var output bytes.Buffer
	logger, err := logging.New(logging.DefaultConfig(), &output)
	if err != nil {
		t.Fatal(err)
	}
	mailboxes := &runtimeTestMailbox{byLane: map[string][]string{}, current: "all"}
	r, err := NewRuntime(config.DefaultCleanerRuntimeConfig(), session, invalidLoggedProcessor{}, &runtimeTestWriter{}, mailboxes, allowReceiveGate{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	r.sourceID = "built_in_bk"
	r.sourceVersion = 19
	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("%v: %s", err, output.String())
	}
	if record["module"] != "cleaner" || record["operation"] != "normalize" || record["event_source_id"] != "built_in_bk" || record["message_id"] != "bad" || record["lane"] != "topic:0" || !strings.Contains(record["error"].(string), "unexpected EOF") {
		t.Fatal(record)
	}
	if len(session.confirmed) != 2 || len(mailboxes.byLane["all"]) != 1 || mailboxes.byLane["all"][0] != "good" {
		t.Fatal("later message was blocked")
	}
}

type failedLoggedMailbox struct{}

func (failedLoggedMailbox) EnqueueBatch(context.Context, []domain.Event) ([]MailboxEnqueueResult, error) {
	return nil, errors.New("Redis READONLY")
}

type failedLoggedWriter struct{}

func (failedLoggedWriter) CreateEvents(context.Context, []domain.Event) ([]store.CreateEventItemResult, error) {
	return nil, errors.New("Elasticsearch unavailable")
}

func TestCleanerPersistenceFailureLogsLocation(t *testing.T) {
	for _, operation := range []string{"event_store", "mailbox_enqueue"} {
		t.Run(operation, func(t *testing.T) {
			var output bytes.Buffer
			logger, err := logging.New(logging.DefaultConfig(), &output)
			if err != nil {
				t.Fatal(err)
			}
			r := &Runtime{logger: logger, sourceID: "source", events: failedLoggedWriter{}, mailboxes: failedLoggedMailbox{}}
			entry := &cleanerEntry{event: domain.Event{BKTenantID: "tenant", EventSourceID: "source", EventID: "event"}, delivery: consume.Delivery{Message: consume.Message{ID: "raw"}, Meta: consume.DeliveryMeta{Lane: "topic:1", Position: "42"}}}
			if operation == "event_store" {
				r.rejectEntry(t.Context(), entry, errors.New("Elasticsearch unavailable"), operation, "event_store_failed")
			} else {
				entry.stored = true
				_, _ = r.enqueueLanePrefix(t.Context(), []*cleanerEntry{entry})
			}
			var record map[string]any
			if err := json.Unmarshal(output.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if record["operation"] != operation || record["error"] == nil || record["event_id"] != "event" || record["bk_tenant_id"] != "tenant" || record["position"] != "42" {
				t.Fatal(record)
			}
		})
	}
}
