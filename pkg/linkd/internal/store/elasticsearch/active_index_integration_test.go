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
	"context"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"linkd/internal/activeindex"
	"linkd/internal/domain"
	"linkd/internal/store/storetest"
)

func TestActiveIndexElasticsearchIntegration(t *testing.T) {
	endpoint := os.Getenv(elasticsearchIntegrationURLEnv)
	if endpoint == "" {
		t.Skip("LINKD_TEST_ELASTICSEARCH_URL is not set")
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	transport := endpointTransport{baseURL: u, client: &http.Client{Timeout: 15 * time.Second}, apiKey: os.Getenv("LINKD_TEST_ELASTICSEARCH_API_KEY")}
	prefix := "linkd-index-" + uuid.NewString()[:8]
	router, err := NewStaticRouter(prefix)
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		for _, target := range router.Targets() {
			_ = r.performJSON(cleanup, http.MethodDelete, "/"+target, nil, nil, nil)
		}
		for _, spec := range router.SchemaConfig().Templates() {
			_ = r.performJSON(cleanup, http.MethodDelete, "/_index_template/"+spec.Name, nil, nil, nil)
		}
	})
	if err := r.EnsureSchema(ctx, router.SchemaConfig()); err != nil {
		t.Fatal(err)
	}
	for _, spec := range router.SchemaConfig().Templates() {
		if err := r.EnsureIndex(ctx, spec.Name, spec.Entity); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct{ tenant, source, id, label string }{{"tenant", "a", "one", "123"}, {"tenant", "b", "two", "123"}, {"tenant", "a", "three", "00123"}, {"other", "a", "four", "123"}} {
		a := storetest.Alert(test.tenant, test.id, "event", test.id, "warning")
		a.EventSourceID = test.source
		a.Labels["strategy_id"] = domain.NewStringScalar(test.label)
		if test.id == "two" {
			a.Labels["strategy_id"], _ = domain.NewNumberScalar(123)
		}
		if _, err := r.CreateAlert(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	q := activeindex.Query{Sources: []string{"a", "b"}, Scope: &activeindex.Scope{BKTenantID: "tenant", StrategyID: "123"}, MaxRows: 10, MaxBytes: 4096}
	rows, err := r.ReadActiveIndex(ctx, q)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%v error=%v", rows, err)
	}
	q.Scope.StrategyID = "00123"
	rows, err = r.ReadActiveIndex(ctx, q)
	if err != nil || len(rows) != 1 {
		t.Fatalf("exact rows=%v error=%v", rows, err)
	}
	q.Scope = nil
	q.MaxRows = 1
	if rows, err := r.ReadActiveIndex(ctx, q); err == nil || rows != nil {
		t.Fatal("truncated snapshot accepted")
	}
	q.MaxRows = 10
	q.MaxBytes = 1
	if rows, err := r.ReadActiveIndex(ctx, q); err == nil || rows != nil {
		t.Fatal("oversize snapshot accepted")
	}
	q.MaxBytes = 4096
	rows, err = r.ReadActiveIndex(ctx, q)
	if err != nil || len(rows) != 4 {
		t.Fatalf("discovery rows=%v error=%v", rows, err)
	}
}
