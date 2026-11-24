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
	"encoding/json"
	"errors"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/consume"
)

func TestNativeFingerprintAndTenantSourceRules(t *testing.T) {
	t.Parallel()
	source := testSource()
	source.FingerprintField = "subject_id" // absent: supplied fingerprint must bypass this rule.
	source.Cleaner.Rules = []config.SourceCleaning{
		{TenantID: "other", Assignments: []config.SourceAssignment{{Target: "title", Field: "extra_data.other", Rule: "term"}}},
		{TenantID: "tenant-1", Assignments: []config.SourceAssignment{{Target: "title", Field: "extra_data.text", Rule: "regexp_extract", Value: "CPU"}}, Filter: json.RawMessage(`{"expression":"A","A":{"field":"title","condition":"term","value":"CPU"}}`)},
	}
	mapper, err := NewMapper(source, config.SeverityConfig{})
	if err != nil {
		t.Fatal(err)
	}
	message := consume.Message{ID: "record-1", TenantID: "tenant-1", EnqueuedAt: time.Now(), Body: []byte(`{"bk_tenant_id":"tenant-1","fingerprint":"given","title":"before","evaluations":[{"action":"triggered","severity":"warning"}],"extra_data":{"text":"CPU 95","other":"wrong"}}`)}
	event, err := mapper.MapMessage(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if event.Fingerprint != "given" || event.Title != "CPU" || event.SourceAlertID != "" {
		t.Fatalf("unexpected event identity or title")
	}
	if string(event.SourceRawData["title"]) != `"before"` {
		t.Fatal("source raw payload was rewritten")
	}
	message.Body = []byte(`{"bk_tenant_id":"tenant-1","fingerprint":"given","title":"before","evaluations":[{"action":"triggered","severity":"warning"}],"extra_data":{"text":"Memory"}}`)
	_, err = mapper.MapMessage(context.Background(), message)
	if !errors.Is(err, ErrFiltered) {
		t.Fatalf("expected normal filter: %v", err)
	}
	message.Headers = map[string][]byte{"kac_ingest_mode": []byte("recovery")}
	event, err = mapper.MapMessage(context.Background(), message)
	if err != nil || event.Title != "before" {
		t.Fatalf("recovery rules not bypassed: %v", err)
	}
}

func TestInvalidSuppliedFingerprintDoesNotFallback(t *testing.T) {
	t.Parallel()
	mapper, err := NewMapper(testSource(), config.SeverityConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, fingerprint := range []string{`""`, `123`, `false`, `null`} {
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(validPayload(), &payload); err != nil {
			t.Fatal(err)
		}
		payload["fingerprint"] = json.RawMessage(fingerprint)
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		_, err = mapper.MapMessage(context.Background(), consume.Message{ID: "record", TenantID: "tenant-1", EnqueuedAt: time.Now(), Body: data})
		if err == nil {
			t.Fatalf("accepted invalid fingerprint %s", fingerprint)
		}
	}
}

func TestSourceRulesPreserveLargeNumericDimensions(t *testing.T) {
	t.Parallel()
	c := StandardCleaner{Rules: []config.SourceCleaning{{Assignments: []config.SourceAssignment{{Target: "extra_data.copied", Field: "dimensions.id", Rule: "term"}}}}}
	payload := []byte(`{"bk_tenant_id":"tenant-1","fingerprint":"stable","title":"CPU","evaluations":[{"action":"triggered","severity":"warning"}],"dimensions":{"id":9007199254740993}}`)
	got, err := c.applyRules(context.Background(), RawEventMessage{BKTenantID: "tenant-1", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(got, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["dimensions"]) != `{"id":9007199254740993}` || string(fields["extra_data"]) != `{"copied":9007199254740993}` {
		t.Fatalf("numeric source fact changed: %s", got)
	}
}

func TestInvalidFingerprintCannotBeHiddenBySourceFiltering(t *testing.T) {
	t.Parallel()
	source := testSource()
	source.Cleaner.Rules = []config.SourceCleaning{{Filter: json.RawMessage(`{"expression":"A","A":{"field":"title","condition":"term","value":"never-matches"}}`)}}
	mapper, err := NewMapper(source, config.SeverityConfig{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = mapper.MapMessage(context.Background(), consume.Message{ID: "record", TenantID: "tenant-1", EnqueuedAt: time.Now(), Body: []byte(`{"fingerprint":null,"title":"CPU","evaluations":[{"action":"triggered","severity":"warning"}]}`)})
	if err == nil || errors.Is(err, ErrFiltered) {
		t.Fatalf("invalid identity treated as filtered: %v", err)
	}
}
