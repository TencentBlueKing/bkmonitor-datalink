// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"linkd/internal/taskdispatch"
)

func TestImportSourceWithDynamicKACSeverity(t *testing.T) {
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin" {
			t.Error("wrong token")
		}
		switch {
		case r.URL.Path == "/api/v1/dynamic-config":
			_, _ = io.WriteString(w, `{"config":{"enabled":true,"current":{"severity":{"default_severity":"fatal","levels":[{"name":"fatal","priority":0}]}}}}`)
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut:
			var mutation taskdispatch.Mutation
			if err := json.NewDecoder(r.Body).Decode(&mutation); err != nil {
				t.Error(err)
			}
			if mutation.Spec.DefaultSeverity != "fatal" {
				t.Error("imported wrong severity")
			}
			puts++
			_, _ = io.WriteString(w, `{"id":"source","published":1}`)
		default:
			t.Error("unexpected request")
		}
	}))
	defer server.Close()
	path := writeCLIConfig(t, fmt.Sprintf("dispatch:\n  url: %s\n  api_token: admin\n", server.URL))
	source := writeCLIConfig(t, `event_sources:
  - event_source_id: source
    related_tenant_id: system
    enabled: true
    cleaner:
      type: standard
    fingerprint_mode: field
    fingerprint_field: source_alert_id
    default_severity: fatal
    storage:
      type: kafka
      kafka:
        brokers: [localhost:9092]
        topic: events
        consumer_group: test
`)
	cmd := NewRootCommand("test", "test", Dependencies{})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--config", path, "event-source", "import", "--file", source})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if puts != 1 {
		t.Fatal("source not published")
	}
}
