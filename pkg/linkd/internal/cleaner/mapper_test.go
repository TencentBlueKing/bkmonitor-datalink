// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cleaner

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/domain"
)

func TestMapperBuildsStableEvent(t *testing.T) {
	t.Parallel()
	source := testSource()
	mapper, err := NewMapper(source, config.SeverityConfig{})
	if err != nil {
		t.Fatal(err)
	}
	message := consume.Message{ID: "topic/0/1", TenantID: "tenant-1", Body: validPayload(), EnqueuedAt: time.Date(2026, 9, 1, 0, 0, 2, 0, time.UTC)}
	first, err := mapper.MapMessage(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	second, err := mapper.MapMessage(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	parsedID, parseErr := domain.ParseEventID(first.EventID)
	if first.EventID != second.EventID || parseErr != nil || parsedID.BKTenantID != first.BKTenantID ||
		parsedID.EventSourceID != source.EventSourceID || first.EventSourceID != source.EventSourceID ||
		first.Fingerprint != "source-alert-1" || first.Severity != "warning" || first.RelatedAlertID != "" {
		t.Fatalf("event=%#v", first)
	}
	if first.BKTenantID != message.TenantID {
		t.Fatalf("Event tenant = %q, want envelope tenant %q", first.BKTenantID, message.TenantID)
	}
	if first.CreateAt != first.ReceivedAt || len(first.SourceRawData) == 0 {
		t.Fatalf("event times/raw=%#v", first)
	}
}

func TestMapperStableIdentityAndTimeFallback(t *testing.T) {
	t.Parallel()
	var payload map[string]any
	if err := json.Unmarshal(validPayload(), &payload); err != nil {
		t.Fatal(err)
	}
	delete(payload, "occurred_at")
	delete(payload, "produced_at")
	delete(payload, "event_id")
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	mapper, err := NewMapper(testSource(), config.SeverityConfig{})
	if err != nil {
		t.Fatal(err)
	}
	receivedAt := time.Date(2026, 9, 1, 0, 0, 2, 123456789, time.UTC)
	message := consume.Message{ID: "stable-record", TenantID: "tenant-1", Body: body, EnqueuedAt: receivedAt}
	first, err := mapper.MapMessage(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	second, err := mapper.MapMessage(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if first.EventID != second.EventID || first.SourceEventID != "" || !first.OccurredAt.Equal(receivedAt) ||
		!first.ProducedAt.Equal(receivedAt) || !first.CreateAt.Equal(receivedAt) {
		t.Fatalf("fallback Event = %#v", first)
	}
	message.ID = "other-record"
	other, err := mapper.MapMessage(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if other.EventID == first.EventID {
		t.Fatal("record ID fallback did not participate in Event ID")
	}
}

func TestMapperTenantOverrideAndTypedFingerprint(t *testing.T) {
	t.Parallel()
	source := testSource()
	source.RelatedTenantID = "forced"
	source.FingerprintMode = config.FingerprintModeFields
	source.FingerprintField = ""
	source.FingerprintFields = []string{"dimensions.host", "subject_id"}
	mapper, err := NewMapper(source, config.SeverityConfig{})
	if err != nil {
		t.Fatal(err)
	}
	message := consume.Message{ID: "record", TenantID: "message-tenant", Body: validPayload(), EnqueuedAt: time.Date(2026, 9, 1, 0, 0, 2, 0, time.UTC)}
	event, err := mapper.MapMessage(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if event.BKTenantID != "forced" || len(event.Fingerprint) != 64 {
		t.Fatalf("event=%#v", event)
	}
	prepared, err := mapper.PrepareMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.TenantID != "forced" || prepared.OrderKey == "" {
		t.Fatalf("prepared=%#v", prepared)
	}
}

func TestMapperAllowsEmptySourceIDsAndPreservesUnknownFields(t *testing.T) {
	t.Parallel()
	var payload map[string]any
	if err := json.Unmarshal(validPayload(), &payload); err != nil {
		t.Fatal(err)
	}
	delete(payload, "event_id")
	delete(payload, "alert_id")
	payload["fingerprint"] = "payload-must-not-win"
	payload["unknown"] = map[string]any{"nested": true}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	source := testSource()
	source.FingerprintField = "subject_id"
	mapper, err := NewMapper(source, config.SeverityConfig{})
	if err != nil {
		t.Fatal(err)
	}
	event, err := mapper.MapMessage(context.Background(), consume.Message{
		ID: "stable-record", TenantID: "tenant-1", Body: body,
		EnqueuedAt: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.SourceEventID != "" || event.SourceAlertID != "" || event.Fingerprint != "1" {
		t.Fatalf("event identities=%#v", event)
	}
	for _, key := range []string{"fingerprint", "unknown"} {
		if _, exists := event.SourceRawData[key]; !exists {
			t.Fatalf("source_raw_data missing %q: %#v", key, event.SourceRawData)
		}
	}
}

func TestMapperRejectsUnstableInputs(t *testing.T) {
	t.Parallel()
	source := testSource()
	source.FingerprintField = "dimensions.missing"
	mapper, err := NewMapper(source, config.SeverityConfig{})
	if err != nil {
		t.Fatal(err)
	}
	message := consume.Message{ID: "record", TenantID: "tenant-1", Body: validPayload(), EnqueuedAt: time.Now()}
	if _, err := mapper.MapMessage(context.Background(), message); err == nil {
		t.Fatal("missing fingerprint dimension accepted")
	}
	message.ID = ""
	source = testSource()
	mapper, _ = NewMapper(source, config.SeverityConfig{})
	if _, err := mapper.MapMessage(context.Background(), message); err == nil {
		t.Fatal("empty record id accepted")
	}
}

func testSource() config.EventSource {
	return config.EventSource{EventSourceID: "source-a", Enabled: true, Cleaner: config.CleanerConfig{Type: config.CleanerTypeStandard}, SeverityMapping: map[string]string{"P2": "warning"}, Storage: config.EventSourceStorageConfig{Type: config.StorageTypeKafka, Kafka: config.KafkaStorageConfig{Brokers: []string{"localhost:9092"}, Topic: "alerts", ConsumerGroup: "linkd"}}}.WithDefaults()
}
