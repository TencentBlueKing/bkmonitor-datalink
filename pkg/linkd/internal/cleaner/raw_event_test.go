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
	"testing"
)

func TestStandardCleanerMapsKnownFields(t *testing.T) {
	t.Parallel()
	cleaner := StandardCleaner{}
	message := RawEventMessage{Payload: validPayload()}
	draft, err := cleaner.Clean(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Action != "triggered" || draft.Title != "CPU high" ||
		draft.SourceEventID != "source-event-1" || draft.SourceAlertID != "source-alert-1" ||
		draft.SubjectSystem != "cmdb" || draft.SubjectType != "host" || draft.SubjectID != "1" {
		t.Fatalf("draft=%#v", draft)
	}
}

func TestStandardCleanerJSONValidationAndUnknownFields(t *testing.T) {
	t.Parallel()
	cleaner := StandardCleaner{}
	message := RawEventMessage{}
	for _, body := range [][]byte{[]byte(`[]`), []byte(`{"title":"x","title":"y"}`), []byte(`{"title":"x"} {}`)} {
		message.Payload = body
		if _, err := cleaner.Clean(context.Background(), message); err == nil {
			t.Fatalf("invalid payload accepted: %s", body)
		}
	}
	message.Payload = []byte(`{"title":"x","severity":"warning"}`)
	if _, err := cleaner.Clean(context.Background(), message); err == nil {
		t.Fatal("standard payload without action was accepted")
	}
	message.Payload = []byte(`{"bk_tenant_id":"payload-tenant","fingerprint":"payload-value","unknown":{"nested":true},"title":"x","action":"triggered"}`)
	draft, err := cleaner.Clean(context.Background(), message)
	if err != nil {
		t.Fatalf("standard payload with unknown fields: %v", err)
	}
	if draft.Title != "x" || draft.SourceEventID != "" || draft.SourceAlertID != "" {
		t.Fatalf("unknown fields affected draft: %#v", draft)
	}
}

func validPayload() []byte {
	return []byte(`{"event_id":"source-event-1","alert_id":"source-alert-1","title":"CPU high","content":"usage high","severity":"P2","action":"triggered","condition_key":"cpu","dimensions":{"host":"host-1"},"subject":{"system":"cmdb","type":"host","id":"1","name":"host-1"},"occurred_at":"2026-09-01T00:00:00Z","produced_at":"2026-09-01T00:00:01Z","labels":{"team":"ops"},"extra_data":{}}`)
}
