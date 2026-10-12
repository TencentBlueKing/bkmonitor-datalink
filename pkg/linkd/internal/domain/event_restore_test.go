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
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"linkd/internal/domain"
)

func TestRestoreEventPreservesHistoricalPayload(t *testing.T) {
	event := validEvent()
	event.SourceRawData = domain.JSONObject{"source": json.RawMessage(` {"z":9007199254740993, "a":[true,null]} `)}
	// 旧结果缺少新版摘要和 Processor 信封，读取和关联更新不消费这些字段。
	event.EnrichStatus = domain.EnrichStatusSucceeded
	event.Enrich.Evaluations = []domain.EvaluationEnrich{{Severity: "warning", Status: domain.EnrichStatusSucceeded, Data: domain.JSONObject{"legacy": json.RawMessage(`{"status":"ok"}`)}}}
	event = event.Clone()
	before := event.Clone()
	restored, err := event.Restore()
	if err != nil {
		t.Fatal(err)
	}
	alertID, err := domain.GenerateAlertID(restored, restored.CreateAt)
	plainID, plainErr := domain.GenerateAlertID(validEvent(), restored.CreateAt)
	if err != nil || plainErr != nil || alertID != plainID {
		t.Fatalf("ID generation depends on unrelated historical payload: %v %v", err, plainErr)
	}
	updated, err := restored.WithRelatedAlertIDs([]string{"b", "a", "a"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updated.SourceRawData, before.SourceRawData) || !reflect.DeepEqual(updated.EventEnrichment, before.EventEnrichment) {
		t.Fatal("state update rewrote historical JSON")
	}
	if !reflect.DeepEqual(updated.RelatedAlertIDs, []string{"a", "b"}) {
		t.Fatal("association was not sorted and deduplicated")
	}
	if _, err := updated.WithRelatedAlertIDs([]string{"other"}); err == nil {
		t.Fatal("frozen association overwritten")
	}
	incoming := before.Clone()
	incoming.EventEnrichment = domain.EventEnrichment{EnrichStatus: domain.EnrichStatusPending}
	if err := domain.ValidateEventRedelivery(incoming, updated); err != nil {
		t.Fatalf("redelivery revalidated historical enrichment: %v", err)
	}
	incoming.Content = "changed fact"
	if err := domain.ValidateEventRedelivery(incoming, updated); err == nil {
		t.Fatal("redelivery overwrote source fact")
	}
	updated.SourceRawData["source"][0] = '!'
	updated.Enrich.Evaluations[0].Data["legacy"][0] = '!'
	if !reflect.DeepEqual(event, before) || !reflect.DeepEqual(restored.SourceRawData, before.SourceRawData) {
		t.Fatal("restored or updated snapshot shares input bytes")
	}
}

func TestRestoreEventDefaultsAndIdentity(t *testing.T) {
	event := validEvent()
	event.OccurredAt, event.ProducedAt, event.CreateAt = time.Time{}, time.Time{}, time.Time{}
	event.ReceivedAt = event.ReceivedAt.In(time.FixedZone("CST", 8*3600))
	event.Dimensions, event.Labels, event.Values, event.SourceRawData, event.ExtraData = nil, nil, nil, nil, nil
	restored, err := event.Restore()
	if err != nil {
		t.Fatal(err)
	}
	if restored.EnrichStatus != domain.EnrichStatusPending || restored.Dimensions == nil || restored.Labels == nil || restored.Values == nil || restored.ExtraData == nil {
		t.Fatal("read defaults missing")
	}
	for _, at := range []time.Time{restored.OccurredAt, restored.ProducedAt, restored.CreateAt, restored.ReceivedAt} {
		if !at.Equal(event.ReceivedAt) || at.Location() != time.UTC {
			t.Fatal("timestamp fallback did not preserve stable received_at")
		}
	}
	for name, change := range map[string]func(*domain.Event){
		"tenant":      func(e *domain.Event) { e.BKTenantID = "" },
		"source":      func(e *domain.Event) { e.EventSourceID = "" },
		"version":     func(e *domain.Event) { e.EventSourceVersion = 0 },
		"event":       func(e *domain.Event) { e.EventID = "" },
		"fingerprint": func(e *domain.Event) { e.Fingerprint = "" },
		"received_at": func(e *domain.Event) { e.ReceivedAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := event.Clone()
			change(&candidate)
			if _, err := candidate.Restore(); err == nil {
				t.Fatal("unsafe missing identity or time was accepted")
			}
		})
	}
}

func TestEventAssociationChecksOnlyChangedFields(t *testing.T) {
	event := validEvent()
	for _, ids := range [][]string{{""}, make([]string, domain.MaxEventEvaluations+1)} {
		if len(ids) > 1 {
			for i := range ids {
				ids[i] = string(rune('a' + i))
			}
		}
		if _, err := event.WithRelatedAlertIDs(ids); err == nil {
			t.Fatal("invalid association accepted")
		}
	}
}
