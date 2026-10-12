// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"encoding/json"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

func TestDecodeHistoricalEventAndUpdateAssociation(t *testing.T) {
	event := storetest.Event("tenant-1", "event-1", "fp", "warning")
	event.EnrichStatus = domain.EnrichStatusSucceeded
	event.EnrichConfigDigest = ""
	event.Enrich.Evaluations = []domain.EvaluationEnrich{{Severity: "warning", Status: domain.EnrichStatusSucceeded, Data: domain.JSONObject{"legacy": json.RawMessage(`{"old_field":true}`)}}}
	event.ExtraData = domain.JSONObject{"counter": json.RawMessage(`9007199254740993`)}
	data, err := encodeEventDocument(event, store.NewUnprocessedEventProcessing())
	if err != nil {
		t.Fatal(err)
	}
	hit := searchHit{Index: "events", ID: documentID(event.BKTenantID, event.EventID), SeqNo: 0, PrimaryTerm: 1, Source: data}
	stored, err := decodeEventHit(hit)
	if err != nil {
		t.Fatal(err)
	}
	updated, _, err := store.ApplyEventResult(stored, store.EventResult{State: domain.EventProcessStateAccepted, RelatedAlertIDs: []string{"alert-1"}, Outcome: "accepted", ProcessedAt: event.ReceivedAt})
	if err != nil || !reflect.DeepEqual(updated.EventEnrichment, event.EventEnrichment) || !reflect.DeepEqual(updated.ExtraData, event.ExtraData) {
		t.Fatalf("historical payload changed or blocked association: %v", err)
	}
	hit.ID = documentID("other-tenant", event.EventID)
	if _, err := decodeEventHit(hit); err == nil {
		t.Fatal("document identity mismatch accepted")
	}
}
