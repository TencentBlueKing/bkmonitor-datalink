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
	"net/http"
	"net/http/httptest"
	"testing"

	"linkd/internal/eventsource"
	"linkd/internal/taskdispatch"
)

func TestCollectorReplayReleaseUsesTaskIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/collector/replay-release" || r.URL.Query().Get("task") != "task / 1" || r.URL.Query().Get("request_id") != "pull/2026-11-14T12:00:00Z" {
			t.Error("recovery identity not encoded")
		}
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("X-Worker-ID") != "worker" {
			t.Error("worker authentication missing")
		}
		_ = json.NewEncoder(w).Encode(eventsource.Release{ID: "source", Version: 3})
	}))
	defer server.Close()
	release, err := collectorReplayRelease(t.Context(), taskdispatch.Client{URL: server.URL, Token: "token", WorkerID: "worker", HTTP: server.Client()}, "task / 1", "pull/2026-11-14T12:00:00Z")
	if err != nil || release.Version != 3 {
		t.Fatalf("recovery release: %+v %v", release, err)
	}
}
