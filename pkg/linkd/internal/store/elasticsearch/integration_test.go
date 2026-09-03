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
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
		return repository
	})
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
		EventBucketDays: 7, AlertHistoryBucketDays: 7, AlertLogBucketDays: 7, MaxFutureSkew: time.Minute,
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
	for index := range total {
		terminal := archiveStoredAlert(t, "integration-"+strconv.Itoa(index), now.Add(time.Duration(index)*time.Millisecond))
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
