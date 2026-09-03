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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

type managerTransport struct {
	mu         sync.Mutex
	indices    map[string]schemaMetadata
	aliases    map[string]map[string]bool
	searches   int
	searchBody string
	lastSearch string
}

func newManagerTransport() *managerTransport {
	return &managerTransport{indices: map[string]schemaMetadata{}, aliases: map[string]map[string]bool{}}
}

func (t *managerTransport) Perform(request *http.Request) (*http.Response, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	path := request.URL.Path
	switch {
	case request.Method == http.MethodPut && strings.HasPrefix(path, "/_index_template/"):
		return managerJSONResponse(http.StatusOK, `{"acknowledged":true}`), nil
	case request.Method == http.MethodPut && strings.Count(strings.TrimPrefix(path, "/"), "/") == 0:
		index := strings.TrimPrefix(path, "/")
		if _, exists := t.indices[index]; exists {
			return managerJSONResponse(http.StatusBadRequest, `{"error":{"type":"resource_already_exists_exception","reason":"exists"}}`), nil
		}
		var body struct {
			Mappings struct {
				Metadata schemaMetadata `json:"_meta"`
			} `json:"mappings"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, err
		}
		t.indices[index] = body.Mappings.Metadata
		return managerJSONResponse(http.StatusOK, `{"acknowledged":true}`), nil
	case request.Method == http.MethodGet && strings.HasSuffix(path, "/_mapping"):
		index := strings.TrimSuffix(strings.TrimPrefix(path, "/"), "/_mapping")
		metadata, exists := t.indices[index]
		if !exists {
			return managerJSONResponse(http.StatusNotFound, `{"error":{"type":"index_not_found_exception","reason":"missing"}}`), nil
		}
		data, _ := json.Marshal(map[string]any{index: map[string]any{"mappings": map[string]any{"_meta": metadata}}})
		return managerBytesResponse(http.StatusOK, data), nil
	case request.Method == http.MethodGet && strings.HasPrefix(path, "/_alias/"):
		alias := strings.TrimPrefix(path, "/_alias/")
		members := t.aliases[alias]
		if len(members) == 0 {
			return managerJSONResponse(http.StatusNotFound, `{"error":{"type":"alias_not_found_exception","reason":"missing"}}`), nil
		}
		response := make(map[string]any, len(members))
		for index, write := range members {
			response[index] = map[string]any{"aliases": map[string]any{alias: map[string]any{"is_write_index": write}}}
		}
		data, _ := json.Marshal(response)
		return managerBytesResponse(http.StatusOK, data), nil
	case request.Method == http.MethodPost && path == "/_aliases":
		var body struct {
			Actions []map[string]map[string]any `json:"actions"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, err
		}
		for _, action := range body.Actions {
			if add := action["add"]; add != nil {
				alias, index := add["alias"].(string), add["index"].(string)
				if t.aliases[alias] == nil {
					t.aliases[alias] = map[string]bool{}
				}
				write, _ := add["is_write_index"].(bool)
				t.aliases[alias][index] = write
			}
			if remove := action["remove"]; remove != nil {
				delete(t.aliases[remove["alias"].(string)], remove["index"].(string))
			}
		}
		return managerJSONResponse(http.StatusOK, `{"acknowledged":true}`), nil
	case request.Method == http.MethodPost && strings.HasSuffix(path, "/_search"):
		t.searches++
		data, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		t.lastSearch = string(data)
		if t.searchBody != "" {
			return managerJSONResponse(http.StatusOK, t.searchBody), nil
		}
		return managerJSONResponse(http.StatusOK, `{"hits":{"hits":[]}}`), nil
	case request.Method == http.MethodPost && path == "/_bulk":
		data, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		items := make([]map[string]any, 0, len(lines))
		for index := 0; index < len(lines); {
			var action map[string]map[string]any
			if err := json.Unmarshal([]byte(lines[index]), &action); err != nil {
				return nil, err
			}
			if metadata := action["create"]; metadata != nil {
				items = append(items, map[string]any{"create": map[string]any{
					"_index": "linkd-test-alert-history-20260831", "_id": metadata["_id"],
					"_seq_no": 0, "_primary_term": 1, "status": http.StatusCreated,
				}})
				index += 2
				continue
			}
			metadata := action["delete"]
			items = append(items, map[string]any{"delete": map[string]any{
				"_index": metadata["_index"], "_id": metadata["_id"], "status": http.StatusOK,
			}})
			index++
		}
		response, _ := json.Marshal(map[string]any{"items": items})
		return managerBytesResponse(http.StatusOK, response), nil
	default:
		return managerJSONResponse(http.StatusBadRequest, `{"error":{"type":"unexpected_request","reason":"`+request.Method+` `+path+`"}}`), nil
	}
}

func TestManagerReconcileSchemaAndActiveDoesNotCreateBucketsOrArchive(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	transport := newManagerTransport()
	router, err := newBucketRouter("linkd-test", BucketConfig{
		EventBucketDays: 7, AlertHistoryBucketDays: 7, AlertLogBucketDays: 7, MaxFutureSkew: 5 * time.Minute,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	repository, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newManager(repository, router, ManagerConfig{
		PrecreatePastBuckets: 1, PrecreateFutureBuckets: 1, MaxBucketsPerEntity: 512,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileSchemaAndActive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if transport.searches != 0 {
		t.Fatalf("terminal alert searches = %d, want 0", transport.searches)
	}
	if len(transport.aliases["linkd-test-alerts-write"]) != 1 {
		t.Fatalf("active write alias=%#v", transport.aliases["linkd-test-alerts-write"])
	}
	if len(transport.indices) != 1 {
		t.Fatalf("indices=%d %#v, want only active alert index", len(transport.indices), transport.indices)
	}
}

func TestManagerIndependentOperationsCreateStableLayout(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	transport := newManagerTransport()
	router, err := newBucketRouter("linkd-test", BucketConfig{
		EventBucketDays: 7, AlertHistoryBucketDays: 7, AlertLogBucketDays: 7, MaxFutureSkew: 5 * time.Minute,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	repository, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newManager(repository, router, ManagerConfig{
		PrecreatePastBuckets: 1, PrecreateFutureBuckets: 1, MaxBucketsPerEntity: 512,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileSchemaAndActive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileBuckets(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileBuckets(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		result, err := manager.ArchiveTerminalAlerts(context.Background(), ArchiveBatchRequest{Limit: 200, WorkerCount: 4})
		if err != nil || result.Archived != 0 {
			t.Fatalf("ArchiveTerminalAlerts() = %#v, %v", result, err)
		}
	}
	if transport.searches != 2 {
		t.Fatalf("terminal alert searches = %d, want 2", transport.searches)
	}
	if len(transport.indices) != 10 {
		t.Fatalf("indices=%d %#v", len(transport.indices), transport.indices)
	}
	for _, alias := range []string{
		"linkd-test-events", "linkd-test-alerts", "linkd-test-alerts-active",
		"linkd-test-alert-history", "linkd-test-alert-logs", "linkd-test-alerts-write",
		"linkd-test-events-write-20260831", "linkd-test-alert-history-write-20260831",
		"linkd-test-alert-logs-write-20260831",
	} {
		if len(transport.aliases[alias]) == 0 {
			t.Errorf("alias %q is missing", alias)
		}
	}
	if len(transport.aliases["linkd-test-alerts-write"]) != 1 ||
		!transport.aliases["linkd-test-alerts-write"]["linkd-test-alerts-active-000001"] {
		t.Fatalf("active write alias=%#v", transport.aliases["linkd-test-alerts-write"])
	}
}

func TestManagerArchiveTerminalAlertsValidatesBatchSize(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	transport := newManagerTransport()
	router, err := newBucketRouter("linkd-test", BucketConfig{
		EventBucketDays: 7, AlertHistoryBucketDays: 7, AlertLogBucketDays: 7,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	repository, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newManager(repository, router, ManagerConfig{
		PrecreatePastBuckets: 1, PrecreateFutureBuckets: 1, MaxBucketsPerEntity: 512,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []ArchiveBatchRequest{
		{Limit: 0, WorkerCount: 1},
		{Limit: 10001, WorkerCount: 1},
		{Limit: 10, WorkerCount: 0},
		{Limit: 10, WorkerCount: 65},
		{Limit: 1, WorkerCount: 2},
	} {
		if _, err := manager.ArchiveTerminalAlerts(context.Background(), request); err == nil {
			t.Fatalf("ArchiveTerminalAlerts(%#v) error = nil", request)
		}
	}
}

func TestManagerArchiveTerminalAlertsDoesNotManageBuckets(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 2, 16, 32, 12, 0, time.UTC)
	eventID, err := domain.GenerateEventID("tenant-1", "source-1", "event-1", now)
	if err != nil {
		t.Fatal(err)
	}
	event := eventForIDTest(t, eventID, now)
	alertID, err := domain.GenerateAlertID(event, event.CreateAt)
	if err != nil {
		t.Fatal(err)
	}
	endAt := now.Add(time.Minute)
	alert := domain.Alert{
		AlertID: alertID, BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID,
		Fingerprint: event.Fingerprint, Severity: event.Severity, Dimensions: domain.DimensionMap{}, Labels: domain.DimensionMap{},
		ExtraData: domain.JSONObject{}, Status: domain.AlertStatusRecovered, LatestEventID: eventID,
		LastOccurredAt: endAt, UpdateAt: endAt, TriggerEventID: eventID, BeginAt: now,
		CreateAt: now, EndAt: &endAt, EndType: domain.AlertEndTypeSource,
		EnrichStatus: domain.EnrichStatusSucceeded, Enrich: domain.JSONObject{},
	}
	document, err := encodeAlertDocument(alert)
	if err != nil {
		t.Fatal(err)
	}
	documentID := alertDocumentID(alert)
	transport := newManagerTransport()
	transport.searchBody = `{"hits":{"hits":[{"_index":"linkd-test-alerts-active-000001","_id":"` +
		documentID + `","_seq_no":7,"_primary_term":2,"_source":` + string(document) + `,"sort":["` + alertID + `"]}]}}`
	router, err := newBucketRouter("linkd-test", BucketConfig{
		EventBucketDays: 7, AlertHistoryBucketDays: 7, AlertLogBucketDays: 7,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	repository, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newManager(repository, router, ManagerConfig{
		PrecreatePastBuckets: 1, PrecreateFutureBuckets: 1, MaxBucketsPerEntity: 512,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.ArchiveTerminalAlerts(
		context.Background(), ArchiveBatchRequest{Limit: 1, WorkerCount: 1, AfterAlertID: "earlier-alert"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Archived != 1 || result.Scanned != 1 || result.Failed != 0 || result.NextCursor != alertID ||
		len(transport.indices) != 0 || len(transport.aliases) != 0 {
		t.Fatalf("result=%#v indices=%#v aliases=%#v", result, transport.indices, transport.aliases)
	}
	if !strings.Contains(transport.lastSearch, `"search_after":["earlier-alert"]`) ||
		!strings.Contains(transport.lastSearch, `"sort":[{"alert_id":{"order":"asc"}}]`) {
		t.Fatalf("search body=%s", transport.lastSearch)
	}
}

func TestManagerArchiveTerminalAlertsUsesBoundedWorkers(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 2, 16, 32, 12, 0, time.UTC)
	terminals := make([]store.StoredAlert, 8)
	for index := range terminals {
		terminals[index] = archiveStoredAlert(t, fmt.Sprintf("worker-%d", index), now.Add(time.Duration(index)*time.Second))
	}
	var mu sync.Mutex
	activeCreates, maximumCreates := 0, 0
	allWorkersStarted := make(chan struct{})
	release := make(chan struct{})
	transport := transportFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		lines := strings.Split(strings.TrimSpace(string(body)), "\n")
		isCreate := strings.Contains(lines[0], `"create"`)
		if isCreate {
			mu.Lock()
			activeCreates++
			maximumCreates = max(maximumCreates, activeCreates)
			if activeCreates == 4 {
				close(allWorkersStarted)
			}
			mu.Unlock()
			<-release
			mu.Lock()
			activeCreates--
			mu.Unlock()
		}
		items := make([]map[string]any, 0)
		step := 1
		if isCreate {
			step = 2
		}
		for index := 0; index < len(lines); index += step {
			var action map[string]map[string]any
			if err := json.Unmarshal([]byte(lines[index]), &action); err != nil {
				return nil, err
			}
			if metadata := action["create"]; metadata != nil {
				items = append(items, map[string]any{"create": map[string]any{
					"_index": "linkd-test-alert-history-20260831", "_id": metadata["_id"],
					"_seq_no": 0, "_primary_term": 1, "status": http.StatusCreated,
				}})
				continue
			}
			metadata := action["delete"]
			items = append(items, map[string]any{"delete": map[string]any{
				"_index": metadata["_index"], "_id": metadata["_id"], "status": http.StatusOK,
			}})
		}
		return jsonResponse(t, map[string]any{"items": items}), nil
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
	manager := &Manager{repository: repository}
	resultChannel := make(chan []archiveItemResult, 1)
	go func() {
		resultChannel <- manager.archiveTerminalAlertsWithWorkers(context.Background(), terminals, 4)
	}()
	select {
	case <-allWorkersStarted:
	case <-time.After(time.Second):
		t.Fatal("four archive workers did not run concurrently")
	}
	close(release)
	results := <-resultChannel
	if maximumCreates != 4 || len(results) != len(terminals) {
		t.Fatalf("maximum workers=%d results=%d", maximumCreates, len(results))
	}
	for _, result := range results {
		if !result.archived || result.err != nil {
			t.Fatalf("archive result=%#v", result)
		}
	}
}

func managerJSONResponse(status int, body string) *http.Response {
	return managerBytesResponse(status, []byte(body))
}

func managerBytesResponse(status int, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}
}
