// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fta

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"go.uber.org/atomic"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

func TestReady(t *testing.T) {
	assert.NotPanics(t, func() {
		Ready(receiver.ComponentConfig{Fta: receiver.ComponentCommon{Enabled: true}})
	})
}

func newSvc(code define.StatusCode, msg string, err error) (HttpService, *atomic.Int64) {
	n := atomic.NewInt64(0)
	svc := HttpService{
		receiver.Publisher{Func: func(record *define.Record) { n.Inc() }},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return code, msg, err
		}},
	}
	return svc, n
}

func TestExportEventCommon(t *testing.T) {
	tests := []struct {
		name          string
		url           string
		headers       map[string]string
		body          string
		wantCode      int
		wantPublished bool
		wantToken     string
		wantData      string
	}{
		{
			name:          "no token",
			url:           "http://localhost/fta/v1/event",
			headers:       map[string]string{},
			body:          `{"test": "1"}`,
			wantCode:      http.StatusForbidden,
			wantPublished: false,
		},
		{
			name:          "query param token",
			url:           "http://localhost/fta/v1/event?token=1&source=tencent",
			headers:       map[string]string{},
			body:          `{"test": "1"}`,
			wantCode:      http.StatusOK,
			wantPublished: true,
			wantToken:     "1",
			wantData:      `{"test":"1", "__http_query_params__":{"source":"tencent"}}`,
		},
		{
			name:          "header token",
			url:           "http://localhost/fta/v1/event",
			headers:       map[string]string{define.KeyToken: "2", "source": "tencent"},
			body:          `{"test": "1"}`,
			wantCode:      http.StatusOK,
			wantPublished: true,
			wantToken:     "2",
			wantData:      `{"__http_headers__":{"Source":"tencent"},"test":"1"}`,
		},
		{
			name:          "error body",
			url:           "http://localhost/fta/v1/event?token=5",
			headers:       map[string]string{},
			body:          `{"test": "1`,
			wantCode:      http.StatusBadRequest,
			wantPublished: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r *define.Record
			svc := HttpService{
				receiver.Publisher{Func: func(record *define.Record) {
					r = record
				}},
				pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
					return define.StatusCodeOK, "", nil
				}},
			}

			buf := bytes.NewBufferString(tt.body)
			req := httptest.NewRequest(http.MethodPost, tt.url, buf)
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}
			rw := httptest.NewRecorder()
			svc.ExportEvent(rw, req)

			assert.Equal(t, tt.wantCode, rw.Code)
			assert.Equal(t, tt.wantPublished, r != nil)

			if tt.wantPublished && r != nil {
				assert.Equal(t, tt.wantToken, r.Token.Original)
				ftaData, ok := r.Data.(*define.FtaData)
				assert.True(t, ok)

				data, _ := json.Marshal(ftaData.Data[0])
				assert.JSONEq(t, tt.wantData, string(data))
			}
		})
	}

	t.Run("broken request", func(t *testing.T) {
		svc, n := newSvc(define.StatusCodeOK, "", nil)
		buf := testkits.NewBrokenReader()
		req := httptest.NewRequest(http.MethodPost, "http://localhost/fta/v1/event?token=5", buf)
		rw := httptest.NewRecorder()
		svc.ExportEvent(rw, req)
		assert.Equal(t, http.StatusBadRequest, rw.Code)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("validator failed", func(t *testing.T) {
		svc, n := newSvc(define.StatusCodeUnauthorized, "", errors.New("MUST ERROR"))
		buf := bytes.NewBufferString(`{"test": "1"}`)
		req := httptest.NewRequest(http.MethodPost, "http://localhost/fta/v1/event?token=5", buf)
		rw := httptest.NewRecorder()
		svc.ExportEvent(rw, req)
		assert.Equal(t, http.StatusUnauthorized, rw.Code)
		assert.Equal(t, int64(0), n.Load())
	})
}
