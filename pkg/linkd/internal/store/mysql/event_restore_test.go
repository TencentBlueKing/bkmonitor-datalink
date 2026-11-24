// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mysqlstore

import (
	"encoding/json"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

func TestDecodeHistoricalEventWithoutRevalidatingPayload(t *testing.T) {
	event := storetest.Event("tenant-1", "event-1", "fp", "warning")
	event.EnrichStatus = domain.EnrichStatusSucceeded
	event.EnrichConfigDigest = ""
	event.Enrich.Evaluations = []domain.EvaluationEnrich{{Severity: "warning", Status: domain.EnrichStatusSucceeded, Data: domain.JSONObject{"legacy": json.RawMessage(`{"old_field":true}`)}}}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := storedEventFromColumns(data, []byte(`{"state":"unprocessed"}`), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ApplyEventResult(stored, store.EventResult{State: domain.EventProcessStateAccepted, RelatedAlertIDs: []string{"alert"}, Outcome: "accepted", ProcessedAt: event.ReceivedAt}); err != nil {
		t.Fatal(err)
	}
}
