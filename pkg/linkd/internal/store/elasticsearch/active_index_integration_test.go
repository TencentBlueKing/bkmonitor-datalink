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
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"linkd/internal/activeindex"
	"linkd/internal/domain"
	"linkd/internal/store/storetest"
)

func activeIndexIntegrationRepository(t *testing.T) (*Repository, context.Context) {
	t.Helper()
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
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	t.Cleanup(cancel)
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
	return r, ctx
}

func TestActiveIndexElasticsearchIntegration(t *testing.T) {
	r, ctx := activeIndexIntegrationRepository(t)
	for _, test := range []struct{ tenant, source, id, label string }{{"tenant", "a", "one", "123"}, {"tenant", "b", "two", "123"}, {"tenant", "a", "three", "00123"}, {"other", "a", "four", "123"}} {
		a := storetest.Alert(test.tenant, test.id, "event", test.id, "warning")
		a.EventSourceID = test.source
		a.Labels["strategy_id"] = domain.NewStringScalar(test.label)
		if test.id == "one" {
			a.Enrich = domain.JSONObject{"processors": json.RawMessage(`[{"fields":{"status":"succeeded","patches":[{"op":"set","path":"$.content","value":"` + strings.Repeat("x", 8192) + `"},{"op":"set","path":"$.labels.strategy_id","value":"123"}]}}]`)}
		}
		if test.id == "two" {
			a.Labels["strategy_id"], _ = domain.NewNumberScalar(123)
			a.Enrich = domain.JSONObject{"processors": json.RawMessage(`[{"strategy":{"status":"succeeded","value":{"strategy_id":123,"unrelated":"` + strings.Repeat("x", 8192) + `"}}}]`)}
		}
		if _, err := r.CreateAlert(ctx, a); err != nil {
			t.Fatal(err)
		}
	}

	q := activeindex.Query{Sources: []string{"a", "b"}, Fields: []string{"labels.strategy_id"}, PageSize: 2, MaxBytes: 4096}
	rows, err := readActiveIndex(r, ctx, q)
	if err != nil || len(rows) != 4 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	labels := map[string]int{}
	for _, row := range rows {
		v, _ := row.Labels["strategy_id"].StringValue()
		if v != "" {
			labels[v]++
		}
		if row.AlertID == "" {
			t.Fatal("repair lost Alert ID")
		}
	}
	if labels["00123"] != 1 {
		t.Fatal("lost original label", labels)
	}
	q.MaxBytes = 1
	if _, err := readActiveIndex(r, ctx, q); err == nil {
		t.Fatal("ignored byte budget")
	}
}
