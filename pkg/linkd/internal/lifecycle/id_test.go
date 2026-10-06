// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"testing"
	"time"

	"linkd/internal/domain"
)

func TestDeterministicAlertID(t *testing.T) {
	event := testEvent("event-1", "warning")
	generator := DeterministicAlertIDGenerator{}
	first, err := generator.Generate(event)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := generator.Generate(event)
	parsed, parseErr := domain.ParseAlertID(first)
	if first != second || first == "" || len(first) > domain.EntityIDMaxBytes || parseErr != nil ||
		parsed.BKTenantID != event.BKTenantID || parsed.EventSourceID != event.EventSourceID {
		t.Fatalf("ids=%q,%q", first, second)
	}
	event.EventID = "event-2"
	third, _ := generator.Generate(event)
	if third == first {
		t.Fatal("different opening events share id")
	}
}

func TestLifecycleIdentityDomains(t *testing.T) {
	event := testEvent("event-1", "warning")
	alert := domain.Alert{AlertID: "alert-1", BKTenantID: event.BKTenantID, UpdateAt: time.Unix(100, 0)}
	cause := AlertChangeCause{Type: AlertChangeCauseSourceEvent, ID: event.EventID}
	result := FinalHookResult{Name: "output", Transport: "kafka", Destination: "alerts", MessageID: "message-1"}
	command := CloseAlertCommand{BKTenantID: event.BKTenantID, AlertID: alert.AlertID, OperationID: "operation-1"}

	identities := map[string]string{
		"event log":       eventAlertLogID(event, alert.AlertID, domain.OperationKindTrigger),
		"operation log":   operationAlertLogID(command),
		"final hook log":  finalHookLogID(cause, alert, OutcomeAlertCreated, result, HookReasonSucceeded),
		"hook invocation": hookInvocationID(cause, alert, OutcomeAlertCreated),
	}
	seen := make(map[string]string, len(identities))
	for name, value := range identities {
		if value == "" {
			t.Fatalf("%s identity is empty", name)
		}
		if previous, exists := seen[value]; exists {
			t.Fatalf("%s and %s share identity %q", name, previous, value)
		}
		seen[value] = name
	}
}
