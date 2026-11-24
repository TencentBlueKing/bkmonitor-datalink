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
	"io"
	"net/http"
	"reflect"
	"testing"
)

func TestEnrichmentMappingAddsOnlyMissingFields(t *testing.T) {
	calls := 0
	transport := transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodPut || request.URL.Path != "/linkd-existing-events/_mapping" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		var body struct {
			Properties map[string]map[string]any `json:"properties"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		expected := eventEnrichmentProperties()
		delete(expected, "enrich_status")
		if !reflect.DeepEqual(body.Properties, expected) {
			t.Fatalf("mapping contains existing fields: %+v", body.Properties)
		}
		return managerJSONResponse(http.StatusOK, `{"acknowledged":true}`), nil
	})
	repo, err := New(transport, mustStaticRouter(t), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	existing := map[string]map[string]any{"enrich_status": keywordProperty(), "source_raw_data": opaqueObjectProperty()}
	if err := repo.ensureEventEnrichmentMapping(context.Background(), "linkd-existing-events", existing); err != nil {
		t.Fatal(err)
	}
	if err := repo.ensureEventEnrichmentMapping(context.Background(), "linkd-existing-events", eventEnrichmentProperties()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("mapping rewritten unnecessarily: %d", calls)
	}
	for _, property := range []map[string]any{{"type": "text"}, {"type": "object", "enabled": true}} {
		existing := eventEnrichmentProperties()
		existing["enrich"] = property
		if err := repo.ensureEventEnrichmentMapping(context.Background(), "linkd-existing-events", existing); err == nil {
			t.Fatal("incompatible mapping accepted")
		}
	}
	if calls != 1 {
		t.Fatal("incompatible mapping was modified")
	}
}

func TestEnrichmentMappingFailureRemainsVisible(t *testing.T) {
	repo, err := New(transportFunc(func(request *http.Request) (*http.Response, error) {
		_, _ = io.Copy(io.Discard, request.Body)
		return managerJSONResponse(http.StatusForbidden, `{"error":{"type":"security_exception","reason":"forbidden"}}`), nil
	}), mustStaticRouter(t), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.ensureEventEnrichmentMapping(context.Background(), "linkd-existing-events", nil); err == nil {
		t.Fatal("mapping write failure hidden")
	}
}
