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
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

func TestReady(t *testing.T) {
	assert.NotPanics(t, Ready)
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

func TestHttpExportMetricsValidateFailed(t *testing.T) {
	buf := &bytes.Buffer{}
	req, err := http.NewRequest(http.MethodPut, "http://localhost/metrics/job/some_job", buf)
	assert.NoError(t, err)

	svc := HttpService{
		receiver.Publisher{Func: func(r *define.Record) {}},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusBadRequest, define.ProcessorTokenChecker, errors.New("MUST ERROR")
		}},
	}

	req = mux.SetURLVars(req, map[string]string{"job": "some_job"})
	rw := httptest.NewRecorder()
	svc.ExportMetrics(rw, req)
	assert.Equal(t, rw.Code, http.StatusBadRequest)
}

func TestHttpExportMetricsNoJob(t *testing.T) {
	buf := &bytes.Buffer{}
	req, err := http.NewRequest(http.MethodPut, "http://localhost/metrics/jox/some_job", buf)
	assert.NoError(t, err)

	svc := HttpService{
		receiver.Publisher{Func: func(r *define.Record) {}},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		}},
	}

	req = mux.SetURLVars(req, map[string]string{"jox": "some_job"})
	rw := httptest.NewRecorder()
	svc.ExportMetrics(rw, req)
	assert.Equal(t, rw.Code, http.StatusBadRequest)
}

func TestHttpExportMetricsInvalidBody(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.Write([]byte("{-}"))
	req, err := http.NewRequest(http.MethodPut, "http://localhost/metrics/job/some_job", buf)
	assert.NoError(t, err)

	svc := HttpService{
		receiver.Publisher{Func: func(r *define.Record) {}},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		}},
	}

	req = mux.SetURLVars(req, map[string]string{"job": "some_job"})
	rw := httptest.NewRecorder()
	svc.ExportMetrics(rw, req)
	assert.Equal(t, rw.Code, http.StatusBadRequest)
}

func TestHttpExportBase64Metrics(t *testing.T) {
	buf := &bytes.Buffer{}
	req, err := http.NewRequest(http.MethodPut, "http://localhost/metrics/job/L3Zhci90bXA", buf)
	assert.NoError(t, err)

	svc := HttpService{
		receiver.Publisher{Func: func(r *define.Record) {}},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		}},
	}

	req = mux.SetURLVars(req, map[string]string{"job": "L3Zhci90bXA"})
	rw := httptest.NewRecorder()
	svc.ExportBase64Metrics(rw, req)
	assert.Equal(t, rw.Code, http.StatusOK)
}

func TestHttpExportBase64MetricsFailed(t *testing.T) {
	buf := &bytes.Buffer{}
	req, err := http.NewRequest(http.MethodPut, "http://localhost/metrics/job/L3Zhci90bXA??", buf)
	assert.NoError(t, err)

	svc := HttpService{
		receiver.Publisher{Func: func(r *define.Record) {}},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		}},
	}

	req = mux.SetURLVars(req, map[string]string{"job": "L3Zhci90bXA??"})
	rw := httptest.NewRecorder()
	svc.ExportBase64Metrics(rw, req)
	assert.Equal(t, rw.Code, http.StatusBadRequest)
}

func TestHttpTokenAfterPreCheck(t *testing.T) {
	buf := &bytes.Buffer{}
	content, err := os.ReadFile("../../example/fixtures/prometheus.txt")
	assert.NoError(t, err)
	buf.Write(content)

	req, err := http.NewRequest(http.MethodPut, "http://localhost/metrics/job/some_job?X-BK-TOKEN=mytoken", buf)
	assert.NoError(t, err)

	var n int
	svc := HttpService{
		receiver.Publisher{Func: func(record *define.Record) { n++ }},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		}},
	}
	rw := httptest.NewRecorder()
	req = mux.SetURLVars(req, map[string]string{"job": "some_job"})
	svc.ExportMetrics(rw, req)

	assert.Equal(t, rw.Code, http.StatusOK)
	assert.Equal(t, 41, n)
}
