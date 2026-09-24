// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"linkd/internal/config"
	"linkd/internal/enrich/preview"
	"linkd/internal/eventsource"
)

type previewDocuments struct{ spec config.EventSource }

func (d previewDocuments) Get(_ context.Context, collection, _ string) (json.RawMessage, string, error) {
	var value any = eventsource.Record{ID: d.spec.EventSourceID, Published: 1, Spec: d.spec}
	if collection == "releases" {
		value = eventsource.Release{ID: d.spec.EventSourceID, Version: 1, Spec: d.spec}
	}
	raw, err := json.Marshal(value)
	return raw, "1", err
}

func (previewDocuments) Put(context.Context, string, string, string, json.RawMessage) error {
	return fmt.Errorf("unexpected write")
}

func (previewDocuments) List(context.Context, string, string, int) ([]json.RawMessage, error) {
	return nil, fmt.Errorf("unexpected list")
}

func TestEnrichPreviewUsesSharedOneModelWithoutOtherResources(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/kingeye_all_instance/_search" || !strings.Contains(string(raw), `"bk_tenant_id":"t"`) {
			t.Errorf("unexpected OneModel query %s: %s", r.URL.Path, raw)
		}
		_, _ = io.WriteString(w, `{"hits":{"hits":[{"_source":{"bk_tenant_id":"t","model_id":"host","model_inst_id":"1","entity_uid":"host|1","attributes":{"owner":"ops"}}}]}}`)
	}))
	defer backend.Close()
	var spec config.EventSource
	err := json.Unmarshal([]byte(`{"event_source_id":"source-a","enrich":{"processors":[{"type":"cmdb","config":{"rules":[{"id":"host","lookup":{"model_id":"host","where":{"field":"model_inst_id","type":"keyword","operator":"eq","value":{"literal":"1"}}},"assignments":[{"target":"$.labels.owner","value":{"jsonpath":"$.lookup.attributes.owner"}}]}]}}]}}`), &spec)
	if err != nil {
		t.Fatal(err)
	}
	resources := config.ResourcesConfig{OneModel: &config.OneModelResource{Addresses: []string{backend.URL}}, MySQL: &config.MySQLResource{Address: "unused:1"}, KingeyeDisplay: &config.DisplayResource{}}
	service := newEnrichPreview(eventsource.New(previewDocuments{spec}, config.SeverityConfig{}), config.StorageConfig{}, resources)
	request := preview.Request{BKTenantID: "t", EventSourceID: "source-a", Input: preview.Input{Alert: json.RawMessage(`{"title":"test","labels":{}}`)}}
	response, err := service.Preview(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || response.EffectiveAlert["labels"].(map[string]any)["owner"] != "ops" {
		t.Fatalf("preview=%+v calls=%d", response, calls.Load())
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), backend.URL) {
		t.Fatal("preview exposed resource configuration")
	}
}
