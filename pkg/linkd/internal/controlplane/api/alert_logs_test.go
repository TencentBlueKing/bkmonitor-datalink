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
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/internaltoken"
	"linkd/internal/store"
)

type alertLogAppendFunc func(context.Context, []domain.AlertLog) ([]store.AppendAlertLogItemResult, error)

func (f alertLogAppendFunc) AppendAlertLogs(ctx context.Context, logs []domain.AlertLog) ([]store.AppendAlertLogItemResult, error) {
	return f(ctx, logs)
}

func TestAppendAlertLogs(t *testing.T) {
	const body = `{"logs":[{"log_id":"one","bk_tenant_id":"tenant-a","alert_id":"alert-one","operator_kind":"user","operation_kind":"dispatched","created_time":"2026-11-13T06:02:21.705123456Z","params":{"content_key":"manual_dispatch.v1","content_params":{"remark":null}}},{"log_id":"two","bk_tenant_id":"tenant-b","alert_id":"alert-two","operator_kind":"system","operation_kind":"notify","created_time":"2026-11-13T06:02:21.705123456Z","params":{}}]}`
	for _, tc := range []struct {
		name       string
		auth       bool
		configured bool
		err        error
		status     int
	}{
		{"partial success", true, true, nil, 200},
		{"unauthenticated", false, true, nil, 401},
		{"not configured", true, false, nil, 503},
		{"invalid batch", true, true, store.ErrInvalidArgument, 400},
		{"transport failure", true, true, errors.New("sensitive transport body"), 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &API{Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}}}
			calls := 0
			if tc.configured {
				a.AlertLogAppender = alertLogAppendFunc(func(ctx context.Context, logs []domain.AlertLog) ([]store.AppendAlertLogItemResult, error) {
					calls++
					if _, ok := ctx.Deadline(); !ok {
						t.Fatal("missing request deadline")
					}
					if len(logs) != 2 || logs[0].BKTenantID != "tenant-a" || logs[1].BKTenantID != "tenant-b" || logs[0].CreatedTime.Nanosecond() != 705123456 {
						t.Fatalf("lost facts: %+v", logs)
					}
					return []store.AppendAlertLogItemResult{{Result: store.AppendAlertLogResult{Created: false}}, {Err: store.ErrIdentityConflict}}, tc.err
				})
			}
			r := httptest.NewRequestWithContext(t.Context(), "POST", "/api/v1/alert-logs", strings.NewReader(body))
			if tc.auth {
				r.Header.Set(internaltoken.HeaderName, testJWT(t, "admin"))
			}
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			if strings.Contains(w.Body.String(), "sensitive") {
				t.Fatal("leaked dependency body")
			}
			if !tc.auth && calls != 0 {
				t.Fatal("called store without auth")
			}
			if tc.status == 200 {
				var out struct {
					Results []alertLogAppendResult `json:"results"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatal(err)
				}
				if len(out.Results) != 2 || out.Results[0].LogID != "one" || out.Results[0].Created || out.Results[0].Error != nil || out.Results[1].LogID != "two" || out.Results[1].Error.Code != "identity_conflict" {
					t.Fatalf("results=%+v", out.Results)
				}
			}
		})
	}
}

func TestAppendAlertLogsRejectsInvalidJSONBeforeStore(t *testing.T) {
	a := &API{Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}}, AlertLogAppender: alertLogAppendFunc(func(context.Context, []domain.AlertLog) ([]store.AppendAlertLogItemResult, error) {
		t.Fatal("unexpected store call")
		return nil, nil
	})}
	r := httptest.NewRequestWithContext(t.Context(), "POST", "/api/v1/alert-logs", strings.NewReader(`{"logs":[]} trailing`))
	r.Header.Set(internaltoken.HeaderName, testJWT(t, "admin"))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("status=%d", w.Code)
	}
}
