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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
)

func migrationKACConfig(address string) config.Config {
	cfg := migrationTestConfig()
	cfg.Plugins.KAC = &config.KACPluginConfig{
		Enabled: true, AlarmEventIndex: "custom_alarm_event",
		Elasticsearch:  config.KACElasticsearchConfig{Addresses: []string{address}},
		ActionEndpoint: "http://unused.invalid/action", JWT: cfg.Dispatch.JWT,
	}
	return cfg
}

func TestMigrationKACStage(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("KAC unavailable")
	for _, mode := range []string{"absent", "disabled", "enabled", "failed"} {
		t.Run(mode, func(t *testing.T) {
			cfg := migrationKACConfig("http://unused.invalid:9200")
			if mode == "absent" {
				cfg.Plugins.KAC = nil
			}
			if mode == "disabled" {
				cfg.Plugins.KAC.Enabled = false
			}
			var calls []string
			step := func(name string) func(context.Context, config.Config) error {
				return func(context.Context, config.Config) error { calls = append(calls, name); return nil }
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			steps := migrationSteps{
				checkRedis: step("redis"), prepareRepository: step("repository"),
				prepareSources: step("sources"), preparePolicies: step("policies"), prepareEventgen: step("eventgen"),
				prepareKAC: func(got context.Context, _ config.Config) error {
					if got != ctx {
						t.Fatal("KAC stage lost migration context")
					}
					calls = append(calls, "kac")
					if mode == "failed" {
						return sentinel
					}
					return nil
				},
			}
			err := runMigration(ctx, cfg, steps)
			want := []string{"redis", "repository", "sources", "policies", "eventgen"}
			switch mode {
			case "enabled":
				want = []string{"redis", "repository", "kac", "sources", "policies", "eventgen"}
			case "failed":
				want = []string{"redis", "repository", "kac"}
				if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "prepare KAC compatibility index") {
					t.Fatalf("error = %v", err)
				}
			}
			if mode != "failed" && err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls=%v, want=%v", calls, want)
			}
		})
	}
}

func TestPrepareMigrationKACInitializesEmptyTargetAndRepeats(t *testing.T) {
	t.Parallel()
	const alias = "custom_alarm_event"
	var template map[string]any
	created, policyWrites, mappingWrites := 0, 0, 0
	date := time.Now().UTC()
	offset := (int(date.Weekday()) + 6) % 7
	bucket := alias + "-" + date.AddDate(0, 0, -offset).Format("20060102")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "PUT /_ilm/policy/" + alias + "_policy":
			policyWrites++
		case "GET /_template/" + alias:
			if template == nil {
				http.Error(w, "missing", http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{alias: template})
			return
		case "PUT /_template/" + alias:
			if err := json.NewDecoder(r.Body).Decode(&template); err != nil {
				t.Error(err)
			}
		case "GET /_alias/" + alias:
			if created == 0 {
				http.Error(w, "missing", http.StatusNotFound)
				return
			}
			_, _ = fmt.Fprintf(w, `{"%s-000001":{"aliases":{"%s":{"is_write_index":true}}}}`, alias, alias)
			return
		case "HEAD /" + bucket:
			if created == 0 {
				w.WriteHeader(http.StatusNotFound)
			}
			return
		case "PUT /" + bucket:
			created++
			var body struct {
				Aliases map[string]struct {
					Write bool `json:"is_write_index"`
				} `json:"aliases"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if _, exists := body.Aliases[alias]; !exists || body.Aliases[alias].Write {
				t.Error("missing read alias or unexpected write index")
			}
		case "PUT /" + alias + "/_mapping":
			mappingWrites++
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"acknowledged":true}`))
	}))
	defer server.Close()
	cfg := migrationKACConfig(server.URL)
	for i := 0; i < 2; i++ {
		if err := prepareMigrationKAC(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
	}
	if created != 1 || policyWrites != 0 || mappingWrites != 2 || template == nil {
		t.Fatalf("created=%d policies=%d mappings=%d template=%v", created, policyWrites, mappingWrites, template != nil)
	}
}

func TestPrepareMigrationKACFailureAndCancellation(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	if err := prepareMigrationKAC(t.Context(), migrationKACConfig(server.URL)); err == nil {
		t.Fatal("storage failure ignored")
	}
	release := make(chan struct{})
	waiting := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { <-release }))
	defer waiting.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := prepareMigrationKAC(ctx, migrationKACConfig(waiting.URL)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
}
