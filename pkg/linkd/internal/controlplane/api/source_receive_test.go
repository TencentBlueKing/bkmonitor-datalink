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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/collector"
	"linkd/internal/logging"
	"linkd/internal/pluginruntime"
)

type receiveExecutor struct{}

func (receiveExecutor) Execute(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
	return pluginruntime.Result{}, nil
}

type receiveIntake struct{ source string }

func (i *receiveIntake) Authorize(_ context.Context, source, secret string) error {
	i.source = source
	if secret != "source-secret" {
		return collector.ErrSourceUnauthorized
	}
	return nil
}

func (i *receiveIntake) Prepare(context.Context, string, string, json.RawMessage) (pluginruntime.Request, *collector.Service, error) {
	return pluginruntime.Request{}, collector.New(receiveExecutor{}, nil, nil), nil
}

func TestSourceReceiveAuthenticationBoundary(t *testing.T) {
	intake := &receiveIntake{}
	logger, err := logging.New(logging.DefaultConfig(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := collector.NewReceiver(intake, 1024, 4, logger)
	if err != nil {
		t.Fatal(err)
	}
	handler := (&API{SourceReceiver: receiver}).Handler()
	for _, tc := range []struct {
		path, secret string
		status       int
	}{
		{"/api/v1/ingest/sources/my-source", "source-secret", 200},
		{"/api/v1/ingest/sources/my-source", "wrong", 401},
		{"/api/v1/ingest/sources/my-source", "", 401},
		{"/api/v1/ingest/preview", "source-secret", 401},
		{"/api/v1/event-sources", "source-secret", 401},
		{"/internal/heartbeat", "source-secret", 401},
	} {
		t.Run(tc.path+tc.secret, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, tc.path, strings.NewReader(`{}`))
			request.Header.Set("X-Secret", tc.secret)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("got %d: %s", response.Code, response.Body.String())
			}
		})
	}
	if intake.source != "my-source" {
		t.Fatal("path source not propagated")
	}
}

func TestSourceReceiveUnavailable(t *testing.T) {
	response := httptest.NewRecorder()
	(&API{}).Handler().ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/ingest/sources/source", strings.NewReader(`{}`)))
	if response.Code != 503 {
		t.Fatal(response.Code)
	}
}
