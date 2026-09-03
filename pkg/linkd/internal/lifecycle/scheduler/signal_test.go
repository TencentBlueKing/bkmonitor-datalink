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
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
)

func TestMailboxSignalRoundTrip(t *testing.T) {
	event := domain.Event{BKTenantID: "tenant-1", EventSourceID: "source-1", Fingerprint: "fp-1"}
	signal := NewSignal(event, time.Unix(100, 0))
	body, err := EncodeSignal(signal)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSignal(body)
	if err != nil {
		t.Fatal(err)
	}
	legacySourceKey := "alarm" + "_" + "source" + "_" + "id"
	if decoded.SchemaVersion != 1 || decoded.EventSourceID != event.EventSourceID ||
		decoded.MailboxID != CorrelationKey(event.BKTenantID, event.EventSourceID, event.Fingerprint) ||
		decoded.MessageID != decoded.MailboxID || decoded.MailboxID == "" ||
		!strings.Contains(string(body), `"event_source_id":"source-1"`) || strings.Contains(string(body), legacySourceKey) {
		t.Fatalf("decoded=%#v", decoded)
	}
}

func TestMailboxSignalRejectsForgedIdentity(t *testing.T) {
	event := domain.Event{BKTenantID: "tenant-1", EventSourceID: "source-1", Fingerprint: "fp-1"}
	signal := NewSignal(event, time.Unix(100, 0))
	signal.MailboxID = "forged"
	if _, err := EncodeSignal(signal); err == nil {
		t.Fatal("EncodeSignal() error = nil")
	}
}

func TestMailboxSignalRejectsTrailingJSON(t *testing.T) {
	event := domain.Event{BKTenantID: "tenant-1", EventSourceID: "source-1", Fingerprint: "fp-1"}
	body, err := EncodeSignal(NewSignal(event, time.Unix(100, 0)))
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, []byte(` {}`)...)
	if _, err := DecodeSignal(body); err == nil {
		t.Fatal("DecodeSignal() accepted trailing JSON")
	}
}

func TestMailboxSignalRejectsLegacySourceField(t *testing.T) {
	event := domain.Event{BKTenantID: "tenant-1", EventSourceID: "source-1", Fingerprint: "fp-1"}
	body, err := EncodeSignal(NewSignal(event, time.Unix(100, 0)))
	if err != nil {
		t.Fatal(err)
	}
	legacySourceKey := "alarm" + "_" + "source" + "_" + "id"
	body = []byte(strings.Replace(string(body), "event_source_id", legacySourceKey, 1))
	if _, err := DecodeSignal(body); err == nil {
		t.Fatal("DecodeSignal() accepted a removed source field")
	}
}

func TestCorrelationKeyScopesTenantAndSource(t *testing.T) {
	base := CorrelationKey("tenant-1", "source-1", "fp")
	if base == CorrelationKey("tenant-2", "source-1", "fp") || base == CorrelationKey("tenant-1", "source-2", "fp") {
		t.Fatal("CorrelationKey() did not scope tenant/source")
	}
}
