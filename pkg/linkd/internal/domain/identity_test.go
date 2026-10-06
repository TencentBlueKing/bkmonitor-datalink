// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain_test

import (
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
)

func TestEventIDRoundTripAndDeterminism(t *testing.T) {
	t.Parallel()
	receivedAt := time.Date(2026, 9, 2, 16, 32, 12, 345678901, time.UTC)
	first, err := domain.GenerateEventID("tenant-infra", "standard-infra", "source-event-1", receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	second, err := domain.GenerateEventID("tenant-infra", "standard-infra", "source-event-1", receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first != "20260902163212.tenant-infra.standard-infra."+first[len(first)-16:] {
		t.Fatalf("event ids=%q,%q", first, second)
	}
	parsed, err := domain.ParseEventID(first)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Timestamp.Equal(receivedAt.Truncate(time.Second)) || parsed.BKTenantID != "tenant-infra" ||
		parsed.EventSourceID != "standard-infra" || len(parsed.Digest) != 16 {
		t.Fatalf("parsed=%#v", parsed)
	}
	other, err := domain.GenerateEventID("tenant-infra", "standard-infra", "source-event-2", receivedAt)
	if err != nil || other == first {
		t.Fatalf("other=%q err=%v", other, err)
	}
}

func TestAlertIDUsesOpeningAnchorAndSeparateDomain(t *testing.T) {
	t.Parallel()
	event := validEvent()
	event.EventID, _ = domain.GenerateEventID(event.BKTenantID, event.EventSourceID, "source-event", event.ReceivedAt)
	alertID, err := domain.GenerateAlertID(event, event.CreateAt)
	if err != nil {
		t.Fatal(err)
	}
	if alertID == event.EventID {
		t.Fatal("event and alert ID domains produced the same identity")
	}
	parsed, err := domain.ParseAlertID(alertID)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Timestamp.Equal(event.CreateAt.Truncate(time.Second)) || parsed.BKTenantID != event.BKTenantID ||
		parsed.EventSourceID != event.EventSourceID {
		t.Fatalf("parsed=%#v", parsed)
	}
}

func TestEntityIDValidation(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"", "20260902163212.tenant.source", "20260902163212.bad.tenant.source.0000000000000000",
		"20261302163212.tenant.source.0000000000000000",
		"20260902163212.tenant.source.000000000000000g",
		strings.Repeat("x", domain.EntityIDMaxBytes+1),
	} {
		if _, err := domain.ParseEventID(value); err == nil {
			t.Fatalf("invalid ID accepted: %q", value)
		}
	}
	if _, err := domain.GenerateEventID("bad.tenant", "source", "stable", time.Now()); err == nil {
		t.Fatal("unsafe tenant accepted")
	}
}
