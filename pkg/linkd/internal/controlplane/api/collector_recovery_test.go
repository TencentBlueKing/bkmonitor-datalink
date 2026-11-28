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
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/pluginruntime"
	"linkd/internal/taskdispatch"
)

type replayDocuments struct {
	emptySources
	releases map[string]eventsource.Release
}

func (d replayDocuments) Get(_ context.Context, kind, key string) (json.RawMessage, string, error) {
	if kind != "releases" {
		return nil, "", eventsource.ErrNotFound
	}
	release, ok := d.releases[key]
	if !ok {
		return nil, "", eventsource.ErrNotFound
	}
	body, err := json.Marshal(release)
	return body, "1", err
}

type executionRequestFunc func(context.Context, string, string, string) (pluginruntime.Request, error)

func (f executionRequestFunc) LoadRequest(ctx context.Context, tenant, source, id string) (pluginruntime.Request, error) {
	return f(ctx, tenant, source, id)
}

func TestCollectorHistoricalReleaseRequiresPersistedRequestAndAssignment(t *testing.T) {
	for _, tc := range []struct {
		name, role, phase, worker, token string
		expired                          bool
		requestTenant, releaseTenant     string
		missing                          bool
		status                           int
	}{
		{"authorized", "collector", "running", "worker", "token", false, "tenant", "tenant", false, 200},
		{"starting authorized", "collector", "starting", "worker", "token", false, "tenant", "tenant", false, 200},
		{"wrong token", "collector", "running", "worker", "wrong", false, "tenant", "tenant", false, 401},
		{"wrong worker", "collector", "running", "other", "token", false, "tenant", "tenant", false, 403},
		{"cleaner history denied", "cleaner", "running", "worker", "token", false, "tenant", "tenant", false, 403},
		{"stopping denied", "collector", "stopping", "worker", "token", false, "tenant", "tenant", false, 403},
		{"expired denied", "collector", "running", "worker", "token", true, "tenant", "tenant", false, 403},
		{"request tenant denied", "collector", "running", "worker", "token", false, "other", "tenant", false, 403},
		{"historical tenant denied", "collector", "running", "worker", "token", false, "tenant", "other", false, 403},
		{"missing request fails", "collector", "running", "worker", "token", false, "tenant", "tenant", true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expires := time.Now().Add(time.Minute)
			if tc.expired {
				expires = time.Now().Add(-time.Minute)
			}
			controller := &workerPolicyController{task: taskdispatch.Task{Source: "source", Worker: "worker", Role: tc.role, Phase: tc.phase, Version: 2, Expires: expires}}
			documents := replayDocuments{releases: map[string]eventsource.Release{
				"source:2": {ID: "source", Version: 2, Spec: config.EventSource{RelatedTenantID: "tenant"}},
				"source:1": {ID: "source", Version: 1, Spec: config.EventSource{RelatedTenantID: tc.releaseTenant, Storage: config.EventSourceStorageConfig{Kafka: config.KafkaStorageConfig{Topic: "original-topic"}}}},
			}}
			lookups := 0
			reader := executionRequestFunc(func(_ context.Context, tenant, source, id string) (pluginruntime.Request, error) {
				lookups++
				if tenant != "tenant" || source != "source" || id != "original" {
					t.Fatal("query parameters overrode assignment")
				}
				if tc.missing {
					return pluginruntime.Request{}, errors.New("request unavailable")
				}
				return pluginruntime.Request{TenantID: tc.requestTenant, SourceID: "source", SourceVersion: 1}, nil
			})
			handler := (&API{Config: config.DispatchConfig{WorkerToken: "token"}, Controller: controller, Sources: eventsource.New(documents, config.SeverityConfig{}), SourceRequests: reader}).Handler()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/internal/collector/replay-release?task=task&request_id=original&tenant=other&version=99", nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			req.Header.Set("X-Worker-ID", tc.worker)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tc.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if tc.status == 200 {
				var release eventsource.Release
				if err := json.Unmarshal(response.Body.Bytes(), &release); err != nil {
					t.Fatal(err)
				}
				if release.Version != 1 || release.Spec.Storage.Kafka.Topic != "original-topic" {
					t.Fatal("wrong replay release")
				}
				if response.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("historical credentials cacheable")
				}
			}
			if (tc.token != "token" || tc.worker != "worker" || tc.role != "collector" || (tc.phase != "running" && tc.phase != "starting") || tc.expired) && lookups != 0 {
				t.Fatal("unauthorized request reached persistent inputs")
			}
		})
	}
}
