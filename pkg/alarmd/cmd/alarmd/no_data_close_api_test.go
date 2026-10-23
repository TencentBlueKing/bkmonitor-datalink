// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type recordedClose struct {
	tenant, strategy, fingerprint string
	at                            time.Time
}

type fakeCloseRecorder struct {
	closes []recordedClose
	err    error
}

func (recorder *fakeCloseRecorder) RecordNoDataManualClose(_ context.Context, tenant, strategy, fingerprint string, at time.Time) error {
	if recorder.err != nil {
		return recorder.err
	}
	recorder.closes = append(recorder.closes, recordedClose{tenant, strategy, fingerprint, at})
	return nil
}

func postNoDataClose(handler http.Handler, method, body string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(method, noDataClosePath, strings.NewReader(body)))
	return response
}

// The close is recorded at the time alarmd received it, and the 200 says
// nothing but that it was recorded.
func TestANoDataCloseIsRecordedAtItsReceiptTime(t *testing.T) {
	received := time.Unix(1_790_000_000, 0)
	recorder := &fakeCloseRecorder{}
	handler := newNoDataCloseAPI(recorder, func() time.Time { return received })
	for _, body := range []string{
		`{"bk_tenant_id":"system","strategy_id":123,"source_alert_id":"0123456789abcdef0123456789abcdef"}`,
		`{"bk_tenant_id":"system","strategy_id":"123","source_alert_id":"0123456789abcdef0123456789abcdef"}`,
	} {
		response := postNoDataClose(handler, http.MethodPost, body)
		if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != "{}" {
			t.Fatalf("%s answered %d %s", body, response.Code, response.Body.String())
		}
	}
	want := recordedClose{"system", "123", "0123456789abcdef0123456789abcdef", received}
	if len(recorder.closes) != 2 || recorder.closes[0] != want || recorder.closes[1] != want {
		t.Fatalf("recorded %+v, want %+v twice", recorder.closes, want)
	}
}

func TestANoDataCloseRefusesWhatItCannotRecord(t *testing.T) {
	for _, testCase := range []struct {
		name, method, body string
		err                error
		status             int
		code               string
	}{
		{"no tenant", http.MethodPost, `{"strategy_id":1,"source_alert_id":"0123456789abcdef0123456789abcdef"}`, nil, 400, "invalid_request"},
		{"zero strategy", http.MethodPost, `{"bk_tenant_id":"s","strategy_id":0,"source_alert_id":"0123456789abcdef0123456789abcdef"}`, nil, 400, "invalid_request"},
		{"not a fingerprint", http.MethodPost, `{"bk_tenant_id":"s","strategy_id":1,"source_alert_id":"abc"}`, nil, 400, "invalid_request"},
		{"an unknown field", http.MethodPost, `{"bk_tenant_id":"s","strategy_id":1,"source_alert_id":"0123456789abcdef0123456789abcdef","closed_at":1}`, nil, 400, "invalid_request"},
		{"not a POST", http.MethodGet, ``, nil, 405, "method_not_allowed"},
		{"the store refuses", http.MethodPost, `{"bk_tenant_id":"s","strategy_id":1,"source_alert_id":"0123456789abcdef0123456789abcdef"}`, errors.New("full"), 503, "store_unavailable"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			handler := newNoDataCloseAPI(&fakeCloseRecorder{err: testCase.err}, time.Now)
			response := postNoDataClose(handler, testCase.method, testCase.body)
			if response.Code != testCase.status || !strings.Contains(response.Body.String(), `"code":"`+testCase.code+`"`) {
				t.Fatalf("answered %d %s, want %d %s", response.Code, response.Body.String(), testCase.status, testCase.code)
			}
		})
	}
}
