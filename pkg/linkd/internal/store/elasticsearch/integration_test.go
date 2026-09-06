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
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

const elasticsearchIntegrationURLEnv = "LINKD_TEST_ELASTICSEARCH_URL"

type endpointTransport struct {
	baseURL *url.URL
	client  *http.Client
	apiKey  string
}

func (t endpointTransport) Perform(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	resolved := *t.baseURL
	resolved.Path = strings.TrimSuffix(t.baseURL.Path, "/") + request.URL.Path
	resolved.RawQuery = request.URL.RawQuery
	cloned.URL = &resolved
	if t.apiKey != "" {
		cloned.Header.Set("Authorization", "ApiKey "+t.apiKey)
	}
	return t.client.Do(cloned)
}

func TestElasticsearchRepositoryContract(t *testing.T) {
	runElasticsearchRepositoryContract(t, false)
}

func TestElasticsearchBatchedRepositoryContract(t *testing.T) {
	runElasticsearchRepositoryContract(t, true)
}

func runElasticsearchRepositoryContract(t *testing.T, batched bool) {
	endpoint := os.Getenv(elasticsearchIntegrationURLEnv)
	if endpoint == "" {
		t.Skipf("set %s to run Elasticsearch contract", elasticsearchIntegrationURLEnv)
	}
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	transport := endpointTransport{baseURL: baseURL, client: &http.Client{Timeout: 15 * time.Second}, apiKey: os.Getenv("LINKD_TEST_ELASTICSEARCH_API_KEY")}
	var sequence atomic.Uint64
	storetest.RunRepositoryContract(t, func(t *testing.T) store.Repository {
		prefix := "linkd-contract-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatUint(sequence.Add(1), 10)
		router, err := NewStaticRouter(prefix)
		if err != nil {
			t.Fatal(err)
		}
		repository, err := New(transport, router, DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		schema := router.SchemaConfig()
		if err := repository.EnsureSchema(ctx, schema); err != nil {
			t.Fatal(err)
		}
		for _, spec := range schema.Templates() {
			if err := repository.EnsureIndex(ctx, spec.Name, spec.Entity); err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			for _, target := range router.Targets() {
				_ = repository.performJSON(cleanup, http.MethodDelete, "/"+target, nil, nil, nil)
			}
			for _, name := range []string{prefix + "-events", prefix + "-alerts", prefix + "-alert-logs"} {
				_ = repository.performJSON(cleanup, http.MethodDelete, "/_index_template/"+name, nil, nil, nil)
			}
		})
		if batched {
			wrapped, closer, err := repository.EnableWriteBatch(WriteBatchConfig{MaxOperations: 32, MaxBytes: 4 << 20, Wait: 2 * time.Millisecond, ReadWait: 10 * time.Millisecond, MaxConcurrentBatches: 2, MaxCalls: 32, Timeout: 15 * time.Second}, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(closer.Close)
			return wrapped
		}
		return repository
	})
}

func TestElasticsearchEventCreateDoesNotDependOnRefresh(t *testing.T) {
	endpoint := os.Getenv(elasticsearchIntegrationURLEnv)
	if endpoint == "" {
		t.Skipf("set %s to run Elasticsearch event refresh integration", elasticsearchIntegrationURLEnv)
	}
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	transport := endpointTransport{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 15 * time.Second},
		apiKey:  os.Getenv("LINKD_TEST_ELASTICSEARCH_API_KEY"),
	}
	prefix := "linkd-event-refresh-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	router, err := NewStaticRouter(prefix)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	schema := router.SchemaConfig()
	if err := repository.EnsureSchema(ctx, schema); err != nil {
		t.Fatal(err)
	}
	if err := repository.EnsureIndex(ctx, router.eventIndex, entityEvent); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_ = repository.performJSON(cleanup, http.MethodDelete, "/"+router.eventIndex, nil, nil, nil)
		for _, spec := range schema.Templates() {
			_ = repository.performJSON(cleanup, http.MethodDelete, "/_index_template/"+spec.Name, nil, nil, nil)
		}
	})
	settings, err := marshalRequest(map[string]any{"index": map[string]any{"refresh_interval": "-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.performJSON(ctx, http.MethodPut, "/"+router.eventIndex+"/_settings", nil, settings, nil); err != nil {
		t.Fatal(err)
	}

	event := storetest.Event("tenant-1", "event-no-refresh", "fingerprint-1", "warning")
	created, err := repository.CreateEvent(ctx, event)
	if err != nil || !created.Created {
		t.Fatalf("CreateEvent()=%#v,%v", created, err)
	}
	stored, err := repository.GetEvent(ctx, event.BKTenantID, event.EventID)
	if err != nil || stored.Event.EventID != event.EventID {
		t.Fatalf("GetEvent()=%#v,%v", stored, err)
	}
	duplicate, err := repository.CreateEvent(ctx, event)
	if err != nil || duplicate.Created {
		t.Fatalf("duplicate CreateEvent()=%#v,%v", duplicate, err)
	}
	duplicates, err := repository.CreateEvents(ctx, []domain.Event{event, event})
	if err != nil || len(duplicates) != 2 {
		t.Fatalf("duplicate batch=%+v error=%v", duplicates, err)
	}
	for _, item := range duplicates {
		if item.Err != nil || item.Result.Created || item.Result.Event.EventID != event.EventID {
			t.Fatalf("realtime duplicate batch item=%+v", item)
		}
	}
	conflict := event.Clone()
	conflict.Title = "different"
	if _, err := repository.CreateEvent(ctx, conflict); !errors.Is(err, store.ErrIdentityConflict) {
		t.Fatalf("conflicting CreateEvent() error=%v", err)
	}

	var count struct {
		Count int `json:"count"`
	}
	if err := repository.performJSON(ctx, http.MethodGet, "/"+router.eventIndex+"/_count", nil, nil, &count); err != nil {
		t.Fatal(err)
	}
	if count.Count != 0 {
		t.Fatalf("event became searchable without refresh: count=%d", count.Count)
	}
	if err := repository.performJSON(ctx, http.MethodPost, "/"+router.eventIndex+"/_refresh", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := repository.performJSON(ctx, http.MethodGet, "/"+router.eventIndex+"/_count", nil, nil, &count); err != nil {
		t.Fatal(err)
	}
	if count.Count != 1 {
		t.Fatalf("event count after refresh=%d", count.Count)
	}
}

func TestElasticsearchLifecycleEventProjectionAndPartialCAS(t *testing.T) {
	endpoint := os.Getenv(elasticsearchIntegrationURLEnv)
	if endpoint == "" {
		t.Skipf("set %s to run Elasticsearch lifecycle event integration", elasticsearchIntegrationURLEnv)
	}
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	for _, batched := range []bool{false, true} {
		t.Run("batched="+strconv.FormatBool(batched), func(t *testing.T) {
			transport := endpointTransport{baseURL: baseURL, client: &http.Client{Timeout: 15 * time.Second}, apiKey: os.Getenv("LINKD_TEST_ELASTICSEARCH_API_KEY")}
			prefix := "linkd-lifecycle-projection-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatBool(batched)
			router, err := NewStaticRouter(prefix)
			if err != nil {
				t.Fatal(err)
			}
			repository, err := New(transport, router, DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			schema := router.SchemaConfig()
			if err := repository.EnsureSchema(ctx, schema); err != nil {
				t.Fatal(err)
			}
			if err := repository.EnsureIndex(ctx, router.eventIndex, entityEvent); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cleanup, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cleanupCancel()
				_ = repository.performJSON(cleanup, http.MethodDelete, "/"+router.eventIndex, nil, nil, nil)
				for _, spec := range schema.Templates() {
					_ = repository.performJSON(cleanup, http.MethodDelete, "/_index_template/"+spec.Name, nil, nil, nil)
				}
			})
			var lifecycleStore store.LifecycleEventStore = repository
			if batched {
				wrapped, closer, err := repository.EnableWriteBatch(WriteBatchConfig{MaxOperations: 32, MaxBytes: 4 << 20, Wait: 2 * time.Millisecond, ReadWait: 10 * time.Millisecond, MaxConcurrentBatches: 2, MaxCalls: 32, Timeout: 15 * time.Second}, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(closer.Close)
				lifecycleStore = wrapped
			}
			event := storetest.Event("tenant-1", "event-projection", "fingerprint-1", "warning")
			event.SourceRawData = domain.JSONObject{"large": json.RawMessage(`{"preserved":true}`)}
			event.ExtraData = domain.JSONObject{"needed": json.RawMessage(`{"value":1}`)}
			created, err := repository.CreateEvent(ctx, event)
			if err != nil || !created.Created {
				t.Fatalf("CreateEvent()=%#v,%v", created, err)
			}
			projected, err := lifecycleStore.GetLifecycleEvent(ctx, event.BKTenantID, event.EventID)
			if err != nil {
				t.Fatal(err)
			}
			if len(projected.Event.SourceRawData) != 0 || len(projected.Event.ExtraData) != 1 {
				t.Fatalf("lifecycle projection=%#v", projected.Event)
			}
			updated, err := lifecycleStore.CompareAndSetLifecycleEventResult(ctx, event.BKTenantID, event.EventID, projected.Version, store.EventResult{
				State: domain.EventProcessStateAccepted, RelatedAlertID: "alert-1", Outcome: "alert_created", ProcessedAt: time.Now().Round(0).UTC(),
			})
			if err != nil || updated.Event.RelatedAlertID != "alert-1" || updated.Processing.State != domain.EventProcessStateAccepted {
				t.Fatalf("CompareAndSetLifecycleEventResult()=%#v,%v", updated, err)
			}
			complete, err := repository.GetEvent(ctx, event.BKTenantID, event.EventID)
			if err != nil {
				t.Fatal(err)
			}
			if string(complete.Event.SourceRawData["large"]) != `{"preserved":true}` || complete.Event.RelatedAlertID != "alert-1" {
				t.Fatalf("complete event after partial CAS=%#v", complete.Event)
			}
		})
	}
}

func TestElasticsearchArchiveDrainsMoreThanOneDefaultBatch(t *testing.T) {
	endpoint := os.Getenv(elasticsearchIntegrationURLEnv)
	if endpoint == "" {
		t.Skipf("set %s to run Elasticsearch archive integration", elasticsearchIntegrationURLEnv)
	}
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	transport := endpointTransport{
		baseURL: baseURL, client: &http.Client{Timeout: 30 * time.Second}, apiKey: os.Getenv("LINKD_TEST_ELASTICSEARCH_API_KEY"),
	}
	prefix := "linkd-archive-" + strconv.Itoa(os.Getpid())
	now := time.Now().Round(0).UTC()
	router, err := newBucketRouter(prefix, BucketConfig{
		EventBucketDays: 7, AlertHistoryBucketDays: 7, AlertLogBucketDays: 7,
		MaxFutureSkew: time.Minute, ActiveAlertRefreshInterval: 5 * time.Second,
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
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := manager.ReconcileSchemaAndActive(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileBuckets(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_ = repository.performJSON(cleanup, http.MethodDelete, "/"+prefix+"-*", nil, nil, nil)
		for _, spec := range router.SchemaConfig().Templates() {
			_ = repository.performJSON(cleanup, http.MethodDelete, "/_index_template/"+spec.Name, nil, nil, nil)
		}
	})

	const total = 1001
	var createBody bytes.Buffer
	var firstTerminal store.StoredAlert
	for index := range total {
		terminal := archiveStoredAlert(t, "integration-"+strconv.Itoa(index), now.Add(time.Duration(index)*time.Millisecond))
		if index == 0 {
			firstTerminal = terminal
		}
		metadata, err := json.Marshal(map[string]any{"create": map[string]string{
			"_index": router.activeAlertWriteAlias(), "_id": alertDocumentID(terminal.Alert),
		}})
		if err != nil {
			t.Fatal(err)
		}
		document, err := encodeAlertDocument(terminal.Alert)
		if err != nil {
			t.Fatal(err)
		}
		createBody.Write(metadata)
		createBody.WriteByte('\n')
		createBody.Write(document)
		createBody.WriteByte('\n')
	}
	var created bulkCreateResponse
	if err := repository.performNDJSON(
		ctx,
		http.MethodPost,
		"/_bulk",
		url.Values{"refresh": []string{"wait_for"}, "require_alias": []string{"true"}},
		createBody.Bytes(),
		&created,
	); err != nil {
		t.Fatal(err)
	}
	if len(created.Items) != total {
		t.Fatalf("bulk created items=%d, want %d", len(created.Items), total)
	}
	for index, item := range created.Items {
		if item.Create.Status != http.StatusCreated {
			t.Fatalf("bulk create item[%d] status=%d", index, item.Create.Status)
		}
	}
	refreshDisabled, err := marshalRequest(map[string]any{"index": map[string]any{"refresh_interval": "-1"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{router.activeAlertIndex(), router.alertHistoryIndex(now)} {
		if err := repository.performJSON(ctx, http.MethodPut, "/"+target+"/_settings", nil, refreshDisabled, nil); err != nil {
			t.Fatal(err)
		}
	}

	first, err := manager.ArchiveTerminalAlerts(ctx, ArchiveBatchRequest{Limit: 1000, WorkerCount: 4})
	if err != nil {
		t.Fatal(err)
	}
	if first.Scanned != 1000 || first.Archived != 1000 || first.Failed != 0 || first.NextCursor == "" {
		t.Fatalf("first archive batch=%#v", first)
	}
	second, err := manager.ArchiveTerminalAlerts(ctx, ArchiveBatchRequest{
		Limit: 1000, WorkerCount: 4, AfterAlertID: first.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Scanned != 1 || second.Archived != 1 || second.Failed != 0 || second.NextCursor != "" {
		t.Fatalf("second archive batch=%#v", second)
	}
	repeated, err := manager.ArchiveTerminalAlerts(ctx, ArchiveBatchRequest{Limit: 1, WorkerCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Scanned != 1 || repeated.Archived != 1 || repeated.Failed != 0 || repeated.NextCursor == "" {
		t.Fatalf("repeated unrefreshed archive batch=%#v", repeated)
	}
	current, err := repository.GetAlertCurrent(
		ctx,
		firstTerminal.Alert.BKTenantID,
		firstTerminal.Alert.AlertID,
	)
	if err != nil || current.Alert.AlertID != firstTerminal.Alert.AlertID {
		t.Fatalf("GetAlertCurrent()=%#v,%v", current, err)
	}

	for target, want := range map[string]int{
		router.activeAlertAlias(): total, router.alertHistoryReadAlias(): 0,
	} {
		var response struct {
			Count int `json:"count"`
		}
		if err := repository.performJSON(ctx, http.MethodGet, "/"+target+"/_count", nil, nil, &response); err != nil {
			t.Fatal(err)
		}
		if response.Count != want {
			t.Fatalf("count before explicit refresh %s=%d, want %d", target, response.Count, want)
		}
	}
	for _, target := range []string{router.activeAlertIndex(), router.alertHistoryIndex(now)} {
		if err := repository.performJSON(ctx, http.MethodPost, "/"+target+"/_refresh", nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}

	for target, want := range map[string]int{
		router.activeAlertAlias(): 0, router.alertHistoryReadAlias(): total,
	} {
		var response struct {
			Count int `json:"count"`
		}
		if err := repository.performJSON(ctx, http.MethodGet, "/"+target+"/_count", nil, nil, &response); err != nil {
			t.Fatal(err)
		}
		if response.Count != want {
			t.Fatalf("count %s=%d, want %d", target, response.Count, want)
		}
	}
}
