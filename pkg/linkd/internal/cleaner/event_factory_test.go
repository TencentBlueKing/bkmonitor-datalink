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
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
)

type recordingSeverityResolver struct {
	value string
	raw   string
}

func (r *recordingSeverityResolver) Resolve(
	_ config.EventSource,
	_ config.SeverityConfig,
	sourceValue string,
) (string, error) {
	r.raw = sourceValue
	return r.value, nil
}

type recordingFingerprintResolver struct {
	value   string
	subject string
}

func (r *recordingFingerprintResolver) Resolve(_ config.EventSource, event domain.Event) (string, error) {
	r.subject = event.SubjectID
	return r.value, nil
}

func TestEventFactoryProtectsSystemFieldsAndUsesResolvers(t *testing.T) {
	t.Parallel()
	severityResolver := &recordingSeverityResolver{value: "critical"}
	fingerprintResolver := &recordingFingerprintResolver{value: "resolved-fingerprint"}
	factory, err := NewEventFactoryWithResolvers(
		testSource(),
		config.SeverityConfig{},
		severityResolver,
		fingerprintResolver,
	)
	if err != nil {
		t.Fatal(err)
	}
	receivedAt := time.Date(2026, 9, 2, 1, 2, 3, 4, time.UTC)
	message := RawEventMessage{
		RecordID: "record-1", BKTenantID: "tenant-1", ReceivedAt: receivedAt,
		Payload: []byte(`{"title":"payload title","fingerprint":"payload-fingerprint","bk_tenant_id":"payload-tenant","unknown":{"nested":true}}`),
	}
	draft := EventDraft{
		Title: "draft title", SourceSeverity: "P0", Action: domain.EventActionTriggered,
		SubjectID: "host-1", Dimensions: domain.DimensionMap{}, Labels: domain.DimensionMap{},
		ExtraData: domain.JSONObject{},
	}
	event, err := factory.Build(message, draft)
	if err != nil {
		t.Fatal(err)
	}
	if severityResolver.raw != "P0" || fingerprintResolver.subject != "host-1" {
		t.Fatalf("resolver inputs severity=%q subject=%q", severityResolver.raw, fingerprintResolver.subject)
	}
	if event.Severity != "critical" || event.Fingerprint != "resolved-fingerprint" ||
		event.BKTenantID != "tenant-1" || event.Title != "draft title" || event.RelatedAlertID != "" {
		t.Fatalf("event=%#v", event)
	}
	for _, key := range []string{"fingerprint", "bk_tenant_id", "unknown"} {
		if _, exists := event.SourceRawData[key]; !exists {
			t.Fatalf("source_raw_data missing %q: %#v", key, event.SourceRawData)
		}
	}
	if !event.OccurredAt.Equal(receivedAt) || !event.ProducedAt.Equal(receivedAt) || !event.CreateAt.Equal(receivedAt) {
		t.Fatalf("event fallback times=%#v", event)
	}
}

func TestEventFactoryRequiresResolvers(t *testing.T) {
	t.Parallel()
	if _, err := NewEventFactoryWithResolvers(testSource(), config.SeverityConfig{}, nil, DefaultFingerprintResolver{}); err == nil {
		t.Fatal("nil severity resolver accepted")
	}
	if _, err := NewEventFactoryWithResolvers(testSource(), config.SeverityConfig{}, DefaultSeverityResolver{}, nil); err == nil {
		t.Fatal("nil fingerprint resolver accepted")
	}
}
