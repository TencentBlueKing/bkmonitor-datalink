// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package taskdispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/kafkaclient"
)

type configurationDocuments struct{ record eventsource.Record }

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
		Processors:  []config.EnrichProcessorConfig{{Type: "source"}},
		DataSources: &config.EnrichDataSources{MySQL: &config.EnrichMySQLDataSource{Password: "secret"}},
	}}}
	cleaner := releaseForRole(release, "cleaner")
	lifecycle := releaseForRole(release, "lifecycle")
	if cleaner.Spec.Enrich.DataSources != nil || len(cleaner.Spec.Enrich.Processors) != 0 {
		t.Fatalf("cleaner release contains enrich configuration: %#v", cleaner.Spec.Enrich)
	}
	if lifecycle.Spec.Enrich.DataSources == nil || lifecycle.Spec.Enrich.DataSources.MySQL.Password != "secret" {
		t.Fatalf("lifecycle release lost enrich configuration: %#v", lifecycle.Spec.Enrich)
	}
	if release.Spec.Enrich.DataSources == nil {
		t.Fatal("releaseForRole changed original release")
	}
}

func TestSourceConfigurationSecrets(t *testing.T) {
	const testMySQLPassword = "private-" + "mysql-secret"
	const testOneModelSecret = "private-" + "onemodel-secret"
	spec := config.EventSource{EventSourceID: "source", Enrich: config.EnrichConfig{DataSources: &config.EnrichDataSources{
		MySQL:         &config.EnrichMySQLDataSource{Address: "mysql:3306", Database: "kingeye", Username: "reader", Password: testMySQLPassword},
		Elasticsearch: &config.EnrichElasticsearchDataSource{Addresses: []string{"http://onemodel:9200"}, IndexPrefix: "bk_monitor_base_", APIKey: testOneModelSecret},
	}}, Storage: config.EventSourceStorageConfig{Type: "kafka", Kafka: config.KafkaStorageConfig{Brokers: []string{"kafka:9092"}, Topic: "raw", ConsumerGroup: "cleaner", Security: kafkaclient.SecurityConfig{Protocol: "sasl_plaintext", SASL: &kafkaclient.SASLConfig{Mechanism: "plain", Username: "reader", Password: "private-kafka-secret"}}}}}
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
				for _, secret := range []string{"private-kafka-secret", testMySQLPassword, testOneModelSecret} {
					if strings.Contains(out.Body.String(), secret) != test.full {
						t.Fatalf("unexpected visibility for %s", secret)
					}
				}
				if test.full && out.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("full configuration must not be cached")
				}
				if test.full && strings.Count(out.Body.String(), testMySQLPassword) != 2 {
					t.Fatal("record and pending release must both be complete")
				}
			})
		}
	}
}
