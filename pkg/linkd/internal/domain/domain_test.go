// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain_test

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
)

func TestScalarJSONAndJSONObject(t *testing.T) {
	t.Parallel()
	for _, data := range []string{`"host"`, `42.5`, `true`} {
		var scalar domain.Scalar
		if err := json.Unmarshal([]byte(data), &scalar); err != nil {
			t.Fatalf("Unmarshal(%s): %v", data, err)
		}
		encoded, _ := json.Marshal(scalar)
		if string(encoded) != data {
			t.Fatalf("round trip = %s", encoded)
		}
	}
	if _, err := domain.NewNumberScalar(math.Inf(1)); err == nil {
		t.Fatal("infinite scalar accepted")
	}
	object := domain.JSONObject{"nested": json.RawMessage(` {"b":2,"a":[true]} `)}
	normalized, err := object.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	cloned := normalized.Clone()
	cloned["nested"][0] = '['
	if reflect.DeepEqual(cloned, normalized) {
		t.Fatal("JSONObject clone shares bytes")
	}
	if _, err := (domain.JSONObject{"x": json.RawMessage(`{"a":1,"a":2}`)}).Normalize(); err == nil {
		t.Fatal("duplicate JSON key accepted")
	}
}

func TestEventNormalizeAndReplacement(t *testing.T) {
	t.Parallel()
	event := validEvent()
	event.OccurredAt = event.OccurredAt.In(time.FixedZone("x", 8*3600))
	normalized, err := event.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if normalized.OccurredAt.Location() != time.UTC {
		t.Fatalf("location=%v", normalized.OccurredAt.Location())
	}
	if err := domain.ValidateNewEvent(normalized); err != nil {
		t.Fatal(err)
	}
	updated, err := normalized.WithRelatedAlertID("alert-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateEventReplacement(normalized, updated); err != nil {
		t.Fatal(err)
	}
	updated.Title = "changed"
	if err := domain.ValidateEventReplacement(normalized, updated); err == nil {
		t.Fatal("source fact mutation accepted")
	}
}

func TestNormalizeCanonicalizesEmptyDynamicFields(t *testing.T) {
	t.Parallel()
	event := validEvent()
	event.Dimensions = nil
	event.Labels = nil
	event.ExtraData = nil
	normalized, err := event.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Dimensions == nil || normalized.Labels == nil || normalized.ExtraData == nil {
		t.Fatalf("empty dynamic fields were not canonicalized: %#v", normalized)
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	legacySourceKey := "alarm" + "_" + "source" + "_" + "id"
	if !strings.Contains(string(encoded), `"event_source_id":"source-a"`) || strings.Contains(string(encoded), legacySourceKey) {
		t.Fatalf("Event JSON source field = %s", encoded)
	}
	var decoded domain.Event
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	decoded, err = decoded.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(normalized, decoded) {
		t.Fatalf("normalized Event changed across JSON round trip: before=%#v after=%#v", normalized, decoded)
	}
}

func TestEventValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*domain.Event)
	}{
		{name: "tenant required", mutate: func(e *domain.Event) { e.BKTenantID = "" }},
		{name: "tenant length", mutate: func(e *domain.Event) { e.BKTenantID = strings.Repeat("t", 65) }},
		{name: "source required", mutate: func(e *domain.Event) { e.EventSourceID = "" }},
		{name: "source format", mutate: func(e *domain.Event) { e.EventSourceID = "bad/source" }},
		{name: "source length", mutate: func(e *domain.Event) { e.EventSourceID = strings.Repeat("s", 33) }},
		{name: "related alert length", mutate: func(e *domain.Event) { e.RelatedAlertID = strings.Repeat("a", 257) }},
		{name: "event id required", mutate: func(e *domain.Event) { e.EventID = "" }},
		{name: "event id length", mutate: func(e *domain.Event) { e.EventID = strings.Repeat("e", domain.EntityIDMaxBytes+1) }},
		{name: "fingerprint required", mutate: func(e *domain.Event) { e.Fingerprint = "" }},
		{name: "fingerprint length", mutate: func(e *domain.Event) { e.Fingerprint = strings.Repeat("f", 129) }},
		{name: "title length", mutate: func(e *domain.Event) { e.Title = strings.Repeat("x", 257) }},
		{name: "content length", mutate: func(e *domain.Event) { e.Content = strings.Repeat("x", 1<<20+1) }},
		{name: "severity required", mutate: func(e *domain.Event) { e.Severity = "" }},
		{name: "severity length", mutate: func(e *domain.Event) { e.Severity = strings.Repeat("s", 33) }},
		{name: "action required", mutate: func(e *domain.Event) { e.Action = "" }},
		{name: "action enum", mutate: func(e *domain.Event) { e.Action = "updated" }},
		{name: "action reason length", mutate: func(e *domain.Event) { e.ActionReason = strings.Repeat("r", 257) }},
		{name: "condition length", mutate: func(e *domain.Event) { e.ConditionKey = strings.Repeat("c", 257) }},
		{name: "subject system length", mutate: func(e *domain.Event) { e.SubjectSystem = strings.Repeat("s", 33) }},
		{name: "subject type length", mutate: func(e *domain.Event) { e.SubjectType = strings.Repeat("s", 129) }},
		{name: "subject id length", mutate: func(e *domain.Event) { e.SubjectID = strings.Repeat("s", 257) }},
		{name: "subject name length", mutate: func(e *domain.Event) { e.SubjectName = strings.Repeat("s", 257) }},
		{name: "source event length", mutate: func(e *domain.Event) { e.SourceEventID = strings.Repeat("s", 257) }},
		{name: "source alert length", mutate: func(e *domain.Event) { e.SourceAlertID = strings.Repeat("s", 257) }},
		{name: "occurred time", mutate: func(e *domain.Event) { e.OccurredAt = time.Time{} }},
		{name: "produced time", mutate: func(e *domain.Event) { e.ProducedAt = time.Time{} }},
		{name: "received time", mutate: func(e *domain.Event) { e.ReceivedAt = time.Time{} }},
		{name: "create time", mutate: func(e *domain.Event) { e.CreateAt = time.Time{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			event := validEvent()
			tt.mutate(&event)
			if _, err := event.Normalize(); err == nil {
				t.Fatal("invalid event accepted")
			}
		})
	}
	event := validEvent()
	event.Title = ""
	if _, err := event.Normalize(); err != nil {
		t.Fatalf("empty title within define.md length was rejected: %v", err)
	}
}

func TestAlertLifecycleValidation(t *testing.T) {
	t.Parallel()
	alert := validAlert()
	normalized, err := alert.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	replacement := normalized.Clone()
	replacement.LatestEventID = "event-2"
	replacement.LastOccurredAt = replacement.LastOccurredAt.Add(-time.Minute)
	replacement.UpdateAt = replacement.UpdateAt.Add(time.Nanosecond)
	if err := domain.ValidateAlertReplacement(normalized, replacement); err != nil {
		t.Fatal(err)
	}
	terminal := replacement.Clone()
	terminal.Status = domain.AlertStatusRecovered
	terminal.EndType = domain.AlertEndTypeSource
	end := terminal.LastOccurredAt
	terminal.EndAt = &end
	terminal.UpdateAt = terminal.UpdateAt.Add(time.Nanosecond)
	if err := domain.ValidateAlertReplacement(replacement, terminal); err != nil {
		t.Fatal(err)
	}
	reopened := terminal.Clone()
	reopened.Status = domain.AlertStatusActive
	reopened.EndAt = nil
	reopened.EndType = ""
	reopened.UpdateAt = reopened.UpdateAt.Add(time.Nanosecond)
	if err := domain.ValidateAlertReplacement(terminal, reopened); err == nil {
		t.Fatal("terminal alert reopened")
	}
	nonMonotonic := normalized.Clone()
	nonMonotonic.LatestEventID = "event-2"
	if err := domain.ValidateAlertReplacement(normalized, nonMonotonic); err == nil {
		t.Fatal("non-increasing update_at was accepted")
	}
	mutatedInherited := normalized.Clone()
	mutatedInherited.Title = "changed"
	mutatedInherited.UpdateAt = mutatedInherited.UpdateAt.Add(time.Nanosecond)
	if err := domain.ValidateAlertReplacement(normalized, mutatedInherited); err == nil {
		t.Fatal("inherited field mutation was accepted")
	}
}

func TestEventAndAlertCloneDynamicFields(t *testing.T) {
	t.Parallel()
	event := validEvent()
	event.Labels["team"] = domain.NewStringScalar("ops")
	event.SourceRawData["nested"] = json.RawMessage(`{"value":1}`)
	event.ExtraData["extra"] = json.RawMessage(`{"value":2}`)
	clonedEvent := event.Clone()
	clonedEvent.Dimensions["host"] = domain.NewStringScalar("changed")
	clonedEvent.Labels["team"] = domain.NewStringScalar("changed")
	clonedEvent.SourceRawData["nested"][0] = '['
	clonedEvent.ExtraData["extra"][0] = '['
	if reflect.DeepEqual(event, clonedEvent) {
		t.Fatal("Event clone shares dynamic fields")
	}
	alert := validAlert()
	alert.Enrich["owner"] = json.RawMessage(`{"id":1}`)
	clonedAlert := alert.Clone()
	clonedAlert.Dimensions["host"] = domain.NewStringScalar("changed")
	clonedAlert.Enrich["owner"][0] = '['
	if reflect.DeepEqual(alert, clonedAlert) {
		t.Fatal("Alert clone shares dynamic fields")
	}
}

func validEvent() domain.Event {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return domain.Event{BKTenantID: "tenant-1", EventSourceID: "source-a", EventID: "event-1", Fingerprint: "fingerprint-1", Title: "CPU high", Severity: "warning", Action: domain.EventActionTriggered, ConditionKey: "cpu", Dimensions: domain.DimensionMap{"host": domain.NewStringScalar("host-1")}, OccurredAt: now, ProducedAt: now, ReceivedAt: now, CreateAt: now, SourceEventID: "source-event-1", SourceAlertID: "source-alert-1", SourceRawData: domain.JSONObject{}, Labels: domain.DimensionMap{}, ExtraData: domain.JSONObject{}}
}

func validAlert() domain.Alert {
	event := validEvent()
	now := event.CreateAt.Add(time.Second)
	return domain.Alert{AlertID: "alert-1", BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID, Fingerprint: event.Fingerprint, Title: event.Title, Severity: event.Severity, ConditionKey: event.ConditionKey, Dimensions: event.Dimensions.Clone(), SourceEventID: event.SourceEventID, SourceAlertID: event.SourceAlertID, Labels: domain.DimensionMap{}, ExtraData: domain.JSONObject{}, Status: domain.AlertStatusActive, LatestEventID: event.EventID, LastOccurredAt: event.OccurredAt, UpdateAt: now, TriggerEventID: event.EventID, BeginAt: event.OccurredAt, CreateAt: now, EnrichStatus: domain.EnrichStatusSucceeded, Enrich: domain.JSONObject{}}
}
