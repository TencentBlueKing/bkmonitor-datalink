// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pushgateway

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"go.uber.org/atomic"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

func TestReady(t *testing.T) {
	assert.NotPanics(t, func() {
		Ready(receiver.ComponentConfig{PushGateway: receiver.ComponentCommon{Enabled: true}})
	})
}

func TestSplitLabels(t *testing.T) {
	scenarios := map[string]struct {
		input          string
		expectError    bool
		expectedOutput map[string]string
	}{
		"regular labels": {
			input: "label_name1/label_value1/label_name2/label_value2",
			expectedOutput: map[string]string{
				"label_name1": "label_value1",
				"label_name2": "label_value2",
			},
		},
		"invalid label name": {
			input:       "label_name1/label_value1/a=b/label_value2",
			expectError: true,
		},
		"reserved label name": {
			input:       "label_name1/label_value1/__label_name2/label_value2",
			expectError: true,
		},
		"unencoded slash in label value": {
			input:       "label_name1/label_value1/label_name2/label/value2",
			expectError: true,
		},
		"encoded slash in first label value ": {
			input: "label_name1@base64/bGFiZWwvdmFsdWUx/label_name2/label_value2",
			expectedOutput: map[string]string{
				"label_name1": "label/value1",
				"label_name2": "label_value2",
			},
		},
		"encoded slash in last label value": {
			input: "label_name1/label_value1/label_name2@base64/bGFiZWwvdmFsdWUy",
			expectedOutput: map[string]string{
				"label_name1": "label_value1",
				"label_name2": "label/value2",
			},
		},
		"encoded slash in last label value with padding": {
			input: "label_name1/label_value1/label_name2@base64/bGFiZWwvdmFsdWUy==",
			expectedOutput: map[string]string{
				"label_name1": "label_value1",
				"label_name2": "label/value2",
			},
		},
		"invalid base64 encoding": {
			input:       "label_name1@base64/foo.bar/label_name2/label_value2",
			expectError: true,
		},
	}

	for name, scenario := range scenarios {
		t.Run(name, func(t *testing.T) {
			parsed, err := splitLabels(scenario.input)
			if err != nil {
				if scenario.expectError {
					return // All good.
				}
				t.Fatalf("Got unexpected error: %s.", err)
			}
			for k, v := range scenario.expectedOutput {
				got, ok := parsed[k]
				if !ok {
					t.Errorf("Expected to find %s=%q.", k, v)
				}
				if got != v {
					t.Errorf("Expected %s=%q but got %s=%q.", k, v, k, got)
				}
				delete(parsed, k)
			}
			for k, v := range parsed {
				t.Errorf("Found unexpected label %s=%q.", k, v)
			}
		})
	}
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

func TestHttpRequest(t *testing.T) {
	t.Run("validate failed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "http://localhost/metrics/job/some_job", &bytes.Buffer{})

		svc, n := newSvc(define.StatusBadRequest, define.ProcessorTokenChecker, errors.New("MUST ERROR"))
		req = mux.SetURLVars(req, map[string]string{"job": "some_job"})
		rw := httptest.NewRecorder()
		svc.ExportMetrics(rw, req)
		assert.Equal(t, rw.Code, http.StatusBadRequest)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("no job", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "http://localhost/metrics/jox/some_job", &bytes.Buffer{})

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		req = mux.SetURLVars(req, map[string]string{"jox": "some_job"})
		rw := httptest.NewRecorder()
		svc.ExportMetrics(rw, req)
		assert.Equal(t, rw.Code, http.StatusBadRequest)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("invalid body", func(t *testing.T) {
		buf := bytes.NewBuffer([]byte("{-}"))
		req := httptest.NewRequest(http.MethodPut, "http://localhost/metrics/job/some_job", buf)

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		req = mux.SetURLVars(req, map[string]string{"job": "some_job"})
		rw := httptest.NewRecorder()
		svc.ExportMetrics(rw, req)
		assert.Equal(t, rw.Code, http.StatusBadRequest)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("base64 url success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "http://localhost/metrics/job/L3Zhci90bXA", &bytes.Buffer{})

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		req = mux.SetURLVars(req, map[string]string{"job": "L3Zhci90bXA"})
		rw := httptest.NewRecorder()
		svc.ExportBase64Metrics(rw, req)
		assert.Equal(t, rw.Code, http.StatusOK)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("base64 url failed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "http://localhost/metrics/job/L3Zhci90bXA??", &bytes.Buffer{})

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		req = mux.SetURLVars(req, map[string]string{"job": "L3Zhci90bXA??"})
		rw := httptest.NewRecorder()
		svc.ExportBase64Metrics(rw, req)
		assert.Equal(t, rw.Code, http.StatusBadRequest)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("report success", func(t *testing.T) {
		content, err := os.ReadFile("../../example/fixtures/prometheus.txt")
		assert.NoError(t, err)
		buf := bytes.NewBuffer(content)
		req := httptest.NewRequest(http.MethodPut, "http://localhost/metrics/job/some_job?X-BK-TOKEN=mytoken", buf)

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		rw := httptest.NewRecorder()
		req = mux.SetURLVars(req, map[string]string{"job": "some_job"})
		svc.ExportMetrics(rw, req)

		assert.Equal(t, rw.Code, http.StatusOK)
		assert.Equal(t, int64(41), n.Load())
	})
}
