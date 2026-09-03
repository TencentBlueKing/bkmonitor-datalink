// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

func TestArchiveTerminalAlertsBulkCreatesHistoryThenConditionallyDeletesActive(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 2, 16, 32, 12, 0, time.UTC)
	eventID, _ := domain.GenerateEventID("tenant-1", "source-1", "event-1", now)
	event := eventForIDTest(t, eventID, now)
	alertID, _ := domain.GenerateAlertID(event, event.CreateAt)
	endAt := now.Add(time.Minute)
	alert := domain.Alert{
		AlertID: alertID, BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID,
		Fingerprint: event.Fingerprint, Severity: event.Severity, Dimensions: domain.DimensionMap{}, Labels: domain.DimensionMap{},
		ExtraData: domain.JSONObject{}, Status: domain.AlertStatusRecovered, LatestEventID: eventID,
		LastOccurredAt: endAt, UpdateAt: endAt, TriggerEventID: eventID, BeginAt: now,
		CreateAt: now, EndAt: &endAt, EndType: domain.AlertEndTypeSource,
		EnrichStatus: domain.EnrichStatusSucceeded, Enrich: domain.JSONObject{},
	}
	activeID := alertDocumentID(alert)
	activeVersion, err := encodeVersion(versionPayload{
		Index: "linkd-test-alerts-active-000001", DocumentID: activeID, SeqNo: 7, PrimaryTerm: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	historyID := documentID(alert.BKTenantID, alert.AlertID)
	if activeID != historyID {
		t.Fatalf("active ID %q differs from history ID %q", activeID, historyID)
	}
	requests := 0
	transport := transportFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		switch requests {
		case 1:
			body, _ := io.ReadAll(request.Body)
			if request.Method != http.MethodPost || request.URL.Path != "/_bulk" ||
				request.URL.Query().Get("require_alias") != "true" ||
				request.URL.Query().Get("refresh") != "wait_for" ||
				!strings.Contains(string(body), `"create":{"_id":"`+historyID+`","_index":"linkd-test-alert-history-write-20260831"}`) {
				t.Fatalf("history request=%s %s", request.Method, request.URL.String())
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
				`{"items":[{"create":{"_index":"linkd-test-alert-history-20260831","_id":"` + historyID + `","_seq_no":0,"_primary_term":1,"status":201}}]}`,
			))}, nil
		case 2:
			body, _ := io.ReadAll(request.Body)
			if request.Method != http.MethodPost || request.URL.Path != "/_bulk" ||
				request.URL.Query().Get("refresh") != "wait_for" ||
				!strings.Contains(string(body), `"if_primary_term":2`) ||
				!strings.Contains(string(body), `"if_seq_no":7`) {
				t.Fatalf("delete request=%s %s", request.Method, request.URL.String())
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
				`{"items":[{"delete":{"_index":"linkd-test-alerts-active-000001","_id":"` + activeID + `","status":200}}]}`,
			))}, nil
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.String())
			return nil, nil
		}
	})
	router, err := newBucketRouter("linkd-test", BucketConfig{
		EventBucketDays: 7, AlertHistoryBucketDays: 7, AlertLogBucketDays: 7, MaxFutureSkew: time.Minute,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	repository, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	results := repository.archiveTerminalAlertsBulk(
		context.Background(), []store.StoredAlert{{Alert: alert, Version: activeVersion}},
	)
	if len(results) != 1 || !results[0].archived || results[0].err != nil || requests != 2 {
		t.Fatalf("results=%#v requests=%d", results, requests)
	}
}

func TestArchiveTerminalAlertsBulkIsolatesCreateConflictAndDeleteFailures(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 2, 16, 32, 12, 0, time.UTC)
	terminals := []store.StoredAlert{
		archiveStoredAlert(t, "created", now),
		archiveStoredAlert(t, "duplicate", now),
		archiveStoredAlert(t, "throttled", now.Add(7*24*time.Hour)),
		archiveStoredAlert(t, "conflict", now.Add(14*24*time.Hour)),
	}
	requests := 0
	transport := transportFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		switch requests {
		case 1:
			body, _ := io.ReadAll(request.Body)
			if !strings.Contains(string(body), "linkd-test-alert-history-write-20260831") ||
				!strings.Contains(string(body), "linkd-test-alert-history-write-20260907") ||
				!strings.Contains(string(body), "linkd-test-alert-history-write-20260914") {
				t.Fatalf("bulk create did not preserve per-alert history routes: %s", body)
			}
			items := make([]map[string]any, 0, len(terminals))
			statuses := []int{http.StatusCreated, http.StatusConflict, http.StatusTooManyRequests, http.StatusConflict}
			for index, terminal := range terminals {
				item := map[string]any{
					"_index": "linkd-test-alert-history-physical", "_id": alertDocumentID(terminal.Alert),
					"_seq_no": 0, "_primary_term": 1, "status": statuses[index],
				}
				if statuses[index] == http.StatusTooManyRequests {
					item["error"] = map[string]string{"type": "es_rejected_execution_exception", "reason": "busy"}
				}
				items = append(items, map[string]any{"create": item})
			}
			return jsonResponse(t, map[string]any{"items": items}), nil
		case 2:
			duplicate := terminals[1].Alert
			conflicting := terminals[3].Alert.Clone()
			conflicting.Title = "different"
			documents := make([]map[string]any, 0, 2)
			for _, alert := range []domain.Alert{duplicate, conflicting} {
				source, err := encodeAlertDocument(alert)
				if err != nil {
					t.Fatal(err)
				}
				documents = append(documents, map[string]any{
					"_index": "linkd-test-alert-history-physical", "_id": alertDocumentID(alert),
					"_seq_no": 0, "_primary_term": 1, "found": true, "_source": json.RawMessage(source),
				})
			}
			return jsonResponse(t, map[string]any{"docs": documents}), nil
		case 3:
			body, _ := io.ReadAll(request.Body)
			createdID := alertDocumentID(terminals[0].Alert)
			duplicateID := alertDocumentID(terminals[1].Alert)
			if !strings.Contains(string(body), createdID) || !strings.Contains(string(body), duplicateID) ||
				strings.Contains(string(body), alertDocumentID(terminals[2].Alert)) ||
				strings.Contains(string(body), alertDocumentID(terminals[3].Alert)) {
				t.Fatalf("delete bulk contains an unsafe item: %s", body)
			}
			createdVersion, _ := decodeVersion(terminals[0].Version)
			duplicateVersion, _ := decodeVersion(terminals[1].Version)
			return jsonResponse(t, map[string]any{"items": []any{
				map[string]any{"delete": map[string]any{
					"_index": createdVersion.Index, "_id": createdVersion.DocumentID, "status": http.StatusOK,
				}},
				map[string]any{"delete": map[string]any{
					"_index": duplicateVersion.Index, "_id": duplicateVersion.DocumentID, "status": http.StatusNotFound,
				}},
			}}), nil
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.String())
			return nil, nil
		}
	})
	router, err := newBucketRouter("linkd-test", BucketConfig{
		EventBucketDays: 7, AlertHistoryBucketDays: 7, AlertLogBucketDays: 7, MaxFutureSkew: 30 * 24 * time.Hour,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	repository, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	results := repository.archiveTerminalAlertsBulk(context.Background(), terminals)
	if len(results) != 4 || !results[0].archived || !results[1].archived ||
		results[2].archived || results[2].stage != "history_create" ||
		results[3].archived || results[3].stage != "history_verify" || requests != 3 {
		t.Fatalf("results=%#v requests=%d", results, requests)
	}
}

func TestSplitArchiveCreateChunksHonorsItemAndByteLimits(t *testing.T) {
	t.Parallel()
	repository := &Repository{config: Config{MaxRequestBytes: 100}}
	prepared := make([]*preparedArchiveItem, store.MaxBatchSize+1)
	indices := make([]int, len(prepared))
	for index := range prepared {
		prepared[index] = &preparedArchiveItem{createMetadata: []byte("{}"), body: make([]byte, 30)}
		indices[index] = index
	}
	chunks := repository.splitArchiveCreateChunks(prepared, indices)
	if len(chunks) != 251 {
		t.Fatalf("byte-limited chunks=%d, want 251", len(chunks))
	}
	repository.config.MaxRequestBytes = 1 << 20
	chunks = repository.splitArchiveCreateChunks(prepared, indices)
	if len(chunks) != 2 || len(chunks[0]) != store.MaxBatchSize || len(chunks[1]) != 1 {
		t.Fatalf("item-limited chunks=%v", []int{len(chunks[0]), len(chunks[1])})
	}
}

func TestTerminalAlertCASLeavesPhysicalArchiveToManager(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 2, 16, 32, 12, 0, time.UTC)
	eventID, _ := domain.GenerateEventID("tenant-1", "source-1", "event-1", now)
	event := eventForIDTest(t, eventID, now)
	alertID, _ := domain.GenerateAlertID(event, event.CreateAt)
	active := domain.Alert{
		AlertID: alertID, BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID,
		Fingerprint: event.Fingerprint, Severity: event.Severity, Dimensions: domain.DimensionMap{}, Labels: domain.DimensionMap{},
		ExtraData: domain.JSONObject{}, Status: domain.AlertStatusActive, LatestEventID: eventID,
		LastOccurredAt: now, UpdateAt: now, TriggerEventID: eventID, BeginAt: now, CreateAt: now,
		EnrichStatus: domain.EnrichStatusSucceeded, Enrich: domain.JSONObject{},
	}
	document, err := encodeAlertDocument(active)
	if err != nil {
		t.Fatal(err)
	}
	documentID := alertDocumentID(active)
	version, err := encodeVersion(versionPayload{
		Index: "linkd-test-alerts-active-000001", DocumentID: documentID, SeqNo: 7, PrimaryTerm: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	transport := transportFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		switch requests {
		case 1:
			if request.Method != http.MethodPost || request.URL.Path != "/_msearch" {
				t.Fatalf("read request=%s %s", request.Method, request.URL.String())
			}
			response := `{"responses":[{"status":200,"hits":{"hits":[{"_index":"linkd-test-alerts-active-000001","_id":"` + documentID + `","_seq_no":7,"_primary_term":2,"_source":` + string(document) + `}]}}]}`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response))}, nil
		case 2:
			if request.Method != http.MethodPut || request.URL.Path != "/linkd-test-alerts-active-000001/_doc/"+documentID ||
				request.URL.Query().Get("refresh") != "wait_for" {
				t.Fatalf("CAS request=%s %s", request.Method, request.URL.String())
			}
			response := `{"_index":"linkd-test-alerts-active-000001","_id":"` + documentID + `","_seq_no":8,"_primary_term":2}`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response))}, nil
		default:
			t.Fatalf("terminal CAS performed synchronous archive request %s %s", request.Method, request.URL.String())
			return nil, nil
		}
	})
	router, err := newBucketRouter("linkd-test", BucketConfig{
		EventBucketDays: 7, AlertHistoryBucketDays: 7, AlertLogBucketDays: 7, MaxFutureSkew: time.Minute,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	repository, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	terminal := active.Clone()
	terminal.Status = domain.AlertStatusRecovered
	terminal.UpdateAt = now.Add(time.Minute)
	terminal.LastOccurredAt = terminal.UpdateAt
	terminal.EndAt = &terminal.UpdateAt
	terminal.EndType = domain.AlertEndTypeSource
	stored, err := repository.CompareAndSetAlert(
		context.Background(), active.BKTenantID, active.AlertID, version, terminal,
	)
	if err != nil {
		t.Fatal(err)
	}
	storedVersion, ok := decodeVersion(stored.Version)
	if !ok || stored.Alert.Status != domain.AlertStatusRecovered ||
		storedVersion.Index != "linkd-test-alerts-active-000001" || requests != 2 {
		t.Fatalf("stored=%#v requests=%d", stored, requests)
	}
}

func archiveStoredAlert(t *testing.T, stableID string, createAt time.Time) store.StoredAlert {
	t.Helper()
	eventID, err := domain.GenerateEventID("tenant-1", "source-1", "event-"+stableID, createAt)
	if err != nil {
		t.Fatal(err)
	}
	event := eventForIDTest(t, eventID, createAt)
	alertID, err := domain.GenerateAlertID(event, event.CreateAt)
	if err != nil {
		t.Fatal(err)
	}
	endAt := createAt.Add(time.Minute)
	alert := domain.Alert{
		AlertID: alertID, BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID,
		Fingerprint: event.Fingerprint, Severity: event.Severity, Dimensions: domain.DimensionMap{}, Labels: domain.DimensionMap{},
		ExtraData: domain.JSONObject{}, Status: domain.AlertStatusRecovered, LatestEventID: eventID,
		LastOccurredAt: endAt, UpdateAt: endAt, TriggerEventID: eventID, BeginAt: createAt,
		CreateAt: createAt, EndAt: &endAt, EndType: domain.AlertEndTypeSource,
		EnrichStatus: domain.EnrichStatusSucceeded, Enrich: domain.JSONObject{},
	}
	version, err := encodeVersion(versionPayload{
		Index: "linkd-test-alerts-active-000001", DocumentID: alertDocumentID(alert), SeqNo: 7, PrimaryTerm: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store.StoredAlert{Alert: alert, Version: version}
}

func jsonResponse(t *testing.T, value any) *http.Response {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body)))}
}
