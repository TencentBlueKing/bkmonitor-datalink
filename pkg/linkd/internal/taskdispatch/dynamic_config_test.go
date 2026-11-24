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
	"net/http"
	"net/http/httptest"
	"testing"

	"linkd/internal/config"
	"linkd/internal/runtimeconfig"
)

func TestWorkerDynamicConfigRetentionAndDisabledNoRequest(t *testing.T) {
	state := runtimeconfig.NewSeverity(config.DefaultSeverityConfig())
	a := &Agent{Severity: state, staticSeverity: state.SeveritySnapshot()}
	requests := 0
	failed := false
	snapshot, err := (runtimeconfig.Snapshot{Enabled: true, NativeNames: true, Severity: config.SeverityConfig{DefaultSeverity: "warning", Levels: []config.SeverityLevel{{Name: "fatal", Priority: 0}, {Name: "warning", Priority: 1}}}}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer worker-token" || r.URL.Path != "/internal/settings/severity" {
			t.Error("invalid worker request")
		}
		if failed {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := json.NewEncoder(w).Encode(snapshot); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client := Client{URL: server.URL, Token: "worker-token"}
	if ready, e := a.syncSeverity(context.Background(), client, "disabled"); !ready || e != "" || requests != 0 {
		t.Fatal("disabled config made request")
	}
	if ready, e := a.syncSeverity(context.Background(), client, snapshot.Digest); !ready || e != "" {
		t.Fatal("snapshot not applied")
	}
	if state.SeveritySnapshot().Digest != snapshot.Digest {
		t.Fatal("wrong config installed")
	}
	if ready, e := a.syncSeverity(context.Background(), client, snapshot.Digest); !ready || e != "" || requests != 1 {
		t.Fatal("unchanged digest fetched again")
	}
	failed = true
	if ready, e := a.syncSeverity(context.Background(), client, "new-digest"); !ready || e != "fetch_failed" || state.SeveritySnapshot().Digest != snapshot.Digest {
		t.Fatal("failure lost installed configuration")
	}
	if ready, e := a.syncSeverity(context.Background(), client, "disabled"); !ready || e != "" || requests != 2 || state.SeveritySnapshot().Enabled {
		t.Fatal("disable did not restore local YAML without fetching")
	}
}

func TestColdWorkerDoesNotStartBeforeFirstDynamicSnapshot(t *testing.T) {
	a := &Agent{Severity: runtimeconfig.NewSeverity(config.DefaultSeverityConfig())}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	if ready, _ := a.syncSeverity(context.Background(), Client{URL: server.URL}, "required"); ready {
		t.Fatal("cold worker admitted without shared configuration")
	}
}

func TestAllInOneSharesSeverityState(t *testing.T) {
	ctx := WithHost(context.Background(), &Host{})
	first := SeverityState(ctx, config.DefaultSeverityConfig())
	second := SeverityState(ctx, config.DefaultSeverityConfig())
	if first != second {
		t.Fatal("all-in-one roles received different states")
	}
}
