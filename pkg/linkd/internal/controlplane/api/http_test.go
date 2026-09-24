// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/config"
	enrichengine "linkd/internal/enrich"
	"linkd/internal/enrich/assembly"
	"linkd/internal/enrich/preview"
	"linkd/internal/eventsource"
	"linkd/internal/kafkaclient"
)

type configurationDocuments struct{ record eventsource.Record }

type publishedDocuments struct {
	configurationDocuments
	release eventsource.Release
}

func (d publishedDocuments) Get(ctx context.Context, collection, key string) (json.RawMessage, string, error) {
	if collection == "releases" {
		b, err := json.Marshal(d.release)
		return b, "1", err
	}
	return d.configurationDocuments.Get(ctx, collection, key)
}

func TestPublishedSourceListPreservesScopeAndRedaction(t *testing.T) {
	draft := config.EventSource{EventSourceID: "source", Hooks: []config.HookConfig{{Name: "active", Type: config.HookTypeActiveAlertByStrategy, Config: config.HookParameters{Redis: &config.RedisConfig{Address: "redis:6379", Password: "draft-secret"}, KeyPrefix: "draft"}}}}
	released := draft.WithDefaults()
	released.Hooks[0].Config.KeyPrefix = "published"
	released.Hooks[0].Config.Redis.Password = "release-secret"
	docs := publishedDocuments{configurationDocuments: configurationDocuments{record: eventsource.Record{ID: "source", Published: 1, Deleted: true, Spec: draft}}, release: eventsource.Release{ID: "source", Version: 1, Spec: released}}
	handler := (&API{Sources: eventsource.New(docs, config.SeverityConfig{}), Config: config.DispatchConfig{APIToken: "admin", WorkerToken: "worker"}}).Handler()
	for _, query := range []string{"?published=true", "?published=true&include_secrets=true"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/event-sources"+query, nil)
		req.Header.Set("Authorization", "Bearer admin")
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		var records []eventsource.Record
		if err := json.Unmarshal(out.Body.Bytes(), &records); err != nil {
			t.Fatal(err)
		}
		if out.Code != 200 || len(records) != 1 || !records[0].Deleted || records[0].Spec.Hooks[0].Config.KeyPrefix != "published" {
			t.Fatalf("unexpected list status=%d", out.Code)
		}
		if strings.Contains(out.Body.String(), "draft-secret") || strings.Contains(out.Body.String(), "release-secret") != strings.Contains(query, "include_secrets=true") {
			t.Fatal("wrong credentials exposed")
		}
	}
}

func (d configurationDocuments) Get(context.Context, string, string) (json.RawMessage, string, error) {
	b, e := json.Marshal(d.record)
	return b, "1", e
}

func (d configurationDocuments) List(context.Context, string, string, int) ([]json.RawMessage, error) {
	b, e := json.Marshal(d.record)
	return []json.RawMessage{b}, e
}

func (configurationDocuments) Put(context.Context, string, string, string, json.RawMessage) error {
	return fmt.Errorf("unexpected write")
}

func TestReleaseForRoleLimitsEnrichCredentials(t *testing.T) {
	t.Parallel()
	release := eventsource.Release{Spec: config.EventSource{Enrich: config.EnrichConfig{
		Processors: []config.EnrichProcessorConfig{{Type: "source"}},
	}}}
	cleaner := releaseForRole(release, "cleaner")
	lifecycle := releaseForRole(release, "lifecycle")
	if len(cleaner.Spec.Enrich.Processors) != 0 {
		t.Fatalf("cleaner release contains enrich configuration: %#v", cleaner.Spec.Enrich)
	}
	if len(lifecycle.Spec.Enrich.Processors) != 1 {
		t.Fatalf("lifecycle release lost enrich configuration: %#v", lifecycle.Spec.Enrich)
	}
	if len(release.Spec.Enrich.Processors) != 1 {
		t.Fatal("releaseForRole changed original release")
	}
}

func TestSourceConfigurationSecrets(t *testing.T) {
	spec := config.EventSource{EventSourceID: "source", Storage: config.EventSourceStorageConfig{Type: "kafka", Kafka: config.KafkaStorageConfig{Brokers: []string{"kafka:9092"}, Topic: "raw", ConsumerGroup: "cleaner", Security: kafkaclient.SecurityConfig{Protocol: "sasl_plaintext", SASL: &kafkaclient.SASLConfig{Mechanism: "plain", Username: "reader", Password: "private-kafka-secret"}}}}}
	record := eventsource.Record{ID: "source", Spec: spec, Pending: &eventsource.Release{ID: "source", Spec: spec}}
	api := (&API{Sources: eventsource.New(configurationDocuments{record}, config.SeverityConfig{}), Config: config.DispatchConfig{APIToken: "admin", WorkerToken: "worker"}}).Handler()
	for _, endpoint := range []string{"/api/v1/event-sources", "/api/v1/event-sources/source"} {
		for _, test := range []struct {
			name, query, token string
			code               int
			full               bool
		}{
			{"default", "", "admin", 200, false},
			{"explicit redacted", "?include_secrets=false", "admin", 200, false},
			{"full", "?include_secrets=true", "admin", 200, true},
			{"invalid", "?include_secrets=yes", "admin", 400, false},
			{"worker denied", "?include_secrets=true", "worker", 401, false},
			{"anonymous denied", "?include_secrets=true", "", 401, false},
		} {
			t.Run(endpoint+"/"+test.name, func(t *testing.T) {
				req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, endpoint+test.query, nil)
				req.Header.Set("Authorization", "Bearer "+test.token)
				out := httptest.NewRecorder()
				api.ServeHTTP(out, req)
				if out.Code != test.code {
					t.Fatalf("status %d want %d", out.Code, test.code)
				}
				for _, secret := range []string{"private-kafka-secret"} {
					if strings.Contains(out.Body.String(), secret) != test.full {
						t.Fatalf("unexpected visibility for %s", secret)
					}
				}
				if test.full && out.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("full configuration must not be cached")
				}
				if test.full && strings.Count(out.Body.String(), "private-kafka-secret") != 2 {
					t.Fatal("record and pending release must both be complete")
				}
			})
		}
	}
}

func TestPreviewHTTPUsesProductionRulesWithoutPublishing(t *testing.T) {
	var enrich config.EnrichConfig
	if err := json.Unmarshal([]byte(`{"processors":[{"type":"fields","config":{"rules":[{"id":"r","operations":[{"id":"set","type":"assign","assignments":[{"target":"$.labels.environment","value":{"literal":"production"}}]}]}]}}]}`), &enrich); err != nil {
		t.Fatal(err)
	}
	source := config.EventSource{EventSourceID: "host", Enrich: enrich}
	documents := publishedDocuments{configurationDocuments: configurationDocuments{record: eventsource.Record{ID: "host", Published: 1}}, release: eventsource.Release{ID: "host", Version: 1, Spec: source}}
	sources := eventsource.New(documents, config.SeverityConfig{})
	handler := (&API{Sources: sources, Previewer: preview.New(sources, nil, func(_ context.Context, source config.EventSource) (preview.Enricher, func() error, error) {
		r, err := assembly.NewRouter([]config.EventSource{source}, enrichengine.Sources{})
		return r, func() error { return nil }, err
	}), Config: config.DispatchConfig{APIToken: "admin", WorkerToken: "worker"}}).Handler()
	body := `{"bk_tenant_id":"t","event_source_id":"host","input":{"alert":{"title":"raw"}}}`
	for _, tc := range []struct {
		token, body string
		status      int
	}{{"worker", body, 401}, {"admin", body, 200}, {"admin", `{"bk_tenant_id":"t","event_source_id":"host","input":{"alert":{},"alert_id":"a"}}`, 400}, {"admin", `{"bk_tenant_id":"t","event_source_id":"host","input":{"alert":{}},"enrich":{"processors":[{"type":"fields","config":{"rules":[{"id":"bad","operations":[{"id":"x","type":"assign","assignments":[{"target":"$.severity","value":{"literal":"fatal"}}]}]}]}}]}}`, 422}} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/enrich/preview", strings.NewReader(tc.body))
		request.Header.Set("Authorization", "Bearer "+tc.token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatalf("code=%d want=%d body=%s", response.Code, tc.status, response.Body.String())
		}
		if tc.status == 200 && !strings.Contains(response.Body.String(), "production") {
			t.Fatal("preview did not run rules")
		}
	}
}
