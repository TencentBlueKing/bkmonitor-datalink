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

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

// TestExportEvent is a generated function returning the mock function for the ExportEvent method of the HttpService type.
func TestExportEvent_Common(t *testing.T) {
	var r *define.Record
	svc := HttpService{
		receiver.Publisher{Func: func(record *define.Record) {
			r = record
		}},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		}},
	}

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
			wantData:      `{"__http_query_params__":{"source":"tencent"},"test":"1"}`,
		},
		{
			name:          "header token",
			url:           "http://localhost/fta/v1/event",
			headers:       map[string]string{tokenKey: "2", "source": "tencent"},
			body:          `{"test": "1"}`,
			wantCode:      http.StatusOK,
			wantPublished: true,
			wantToken:     "2",
			wantData:      `{"__http_headers__":{"Source":"tencent"},"test":"1"}`,
		},
		{
			name:          "header fta token",
			url:           "http://localhost/fta/v1/event",
			headers:       map[string]string{ftaTokenKey: "3"},
			body:          `{"test": "1"}`,
			wantCode:      http.StatusOK,
			wantPublished: true,
			wantToken:     "3",
			wantData:      `{"test":"1"}`,
		},
		{
			name:          "plugin id",
			url:           "http://localhost/fta/v1/event/test?token=4",
			headers:       map[string]string{},
			body:          `{"test": "1"}`,
			wantCode:      http.StatusOK,
			wantPublished: true,
			wantToken:     "4",
			wantData:      `{"test":"1"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// reset
			r = nil

			// run
			buf := bytes.NewBufferString(tt.body)
			req, _ := http.NewRequest(http.MethodPost, tt.url, buf)
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}
			rw := httptest.NewRecorder()
			svc.ExportEvent(rw, req)

			// assert status
			assert.Equal(t, tt.wantCode, rw.Code)
			assert.Equal(t, tt.wantPublished, r != nil)

			// assert record
			if tt.wantPublished && r != nil {
				assert.Equal(t, tt.wantToken, r.Token.Original)
				ftaData, ok := r.Data.(*define.FtaData)
				assert.True(t, ok)

				data, err := json.Marshal(ftaData.Data[0])
				assert.NoError(t, err)

				assert.Equal(t, tt.wantData, string(data))
			}
		})
	}
}
