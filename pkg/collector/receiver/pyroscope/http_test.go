// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pyroscope

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

func TestReady(t *testing.T) {
	assert.NotPanics(t, Ready)
}

func TestHttpBadRequest(t *testing.T) {
	t.Run("Invalid Params", func(t *testing.T) {
		buf := &bytes.Buffer{}
		req, err := http.NewRequest(http.MethodPost, "http://localhost/pyroscope/ingest", buf)
		assert.NoError(t, err)

		var n int
		svc := HttpService{
			receiver.Publisher{Func: func(record *define.Record) { n++ }},
			pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
				return define.StatusCodeOK, "", nil
			}},
		}
		rw := httptest.NewRecorder()
		svc.ProfilesIngest(rw, req)
		assert.Equal(t, http.StatusBadRequest, rw.Code)
		assert.Equal(t, 0, n)
	})

	t.Run("Invalid SpyName", func(t *testing.T) {
		buf := &bytes.Buffer{}
		req, err := http.NewRequest(http.MethodPost, "http://localhost/pyroscope/ingest?aggregationType=sum&from=1698053090&name=fuxi%7B%7D&sampleRate=100&spyName=hahaha&units=samples&until=1698053100", buf)
		assert.NoError(t, err)

		var n int
		svc := HttpService{
			receiver.Publisher{Func: func(record *define.Record) { n++ }},
			pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
				return define.StatusCodeOK, "", nil
			}},
		}
		rw := httptest.NewRecorder()
		svc.ProfilesIngest(rw, req)
		assert.Equal(t, http.StatusBadRequest, rw.Code)
		assert.Equal(t, 0, n)
	})

	t.Run("Invalid Body", func(t *testing.T) {
		buf := &bytes.Buffer{}
		buf.WriteString("{-}")
		req, _ := http.NewRequest(http.MethodPost, "http://localhost/pyroscope/ingest?aggregationType=sum&from=1698053090&name=fuxi%7B%7D&sampleRate=100&spyName=javaspy&units=samples&until=1698053100", buf)

		var n int
		svc := HttpService{
			receiver.Publisher{Func: func(record *define.Record) { n++ }},
			pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
				return define.StatusCodeOK, "", nil
			}},
		}
		rw := httptest.NewRecorder()
		svc.ProfilesIngest(rw, req)
		assert.Equal(t, http.StatusBadRequest, rw.Code)
		assert.Equal(t, 0, n)
	})

	t.Run("Broken Request", func(t *testing.T) {
		svc := HttpService{
			receiver.Publisher{Func: func(record *define.Record) {}},
			pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
				return define.StatusCodeOK, "", nil
			}},
		}
		buf := testkits.NewBrokenReader()
		req, _ := http.NewRequest(http.MethodPost, "localhost", buf)
		rw := httptest.NewRecorder()
		svc.ProfilesIngest(rw, req)
		assert.Equal(t, http.StatusBadRequest, rw.Code)
	})

	t.Run("Valid Failed", func(t *testing.T) {
		svc := HttpService{
			receiver.Publisher{Func: func(record *define.Record) {}},
			pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
				return define.StatusCodeUnauthorized, "", errors.New("MUST ERROR")
			}},
		}

		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		fw, err := writer.CreateFormFile("profile", "profile.pprof")
		assert.NoError(t, err)

		_, err = fw.Write([]byte("any profiles"))
		assert.NoError(t, err)
		assert.NoError(t, writer.Close())

		req, _ := http.NewRequest(http.MethodPost, "http://localhost/pyroscope/ingest?aggregationType=sum&from=1698053090&name=fuxi%7B%7D&sampleRate=100&spyName=javaspy&units=samples&until=1698053100", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("Authorization", "Bearer token_instance")

		rw := httptest.NewRecorder()
		svc.ProfilesIngest(rw, req)
		assert.Equal(t, http.StatusUnauthorized, rw.Code)
	})
}

func TestStatusOk(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	fw, err := writer.CreateFormFile("profile", "profile.pprof")
	assert.NoError(t, err)

	_, err = fw.Write([]byte("any profiles"))
	assert.NoError(t, err)
	assert.NoError(t, writer.Close())

	req, err := http.NewRequest(http.MethodPost, "http://localhost/pyroscope/ingest?aggregationType=sum&from=1698053090&name=fuxi&sampleRate=100&spyName=gospy&units=samples&until=1698053100", body)
	assert.NoError(t, err)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer token_instance")

	var n int
	svc := HttpService{
		receiver.Publisher{Func: func(record *define.Record) { n++ }},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		}},
	}
	rw := httptest.NewRecorder()
	svc.ProfilesIngest(rw, req)
	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, 1, n)
}

func TestGetBearerToken(t *testing.T) {
	t.Run("valid token", func(t *testing.T) {
		expectedToken := "test_token"
		req, _ := http.NewRequest("GET", "localhost", nil)
		req.Header.Set("Authorization", "Bearer "+expectedToken)
		assert.Equal(t, getBearerToken(req), expectedToken)
	})

	t.Run("invalid data", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "localhost", nil)
		req.Header.Set("Authorization", "Basic some_base64_credentials")
		assert.Empty(t, getBearerToken(req))
	})

	t.Run("no auth header", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "localhost", nil)
		assert.Empty(t, getBearerToken(req))
	})
}

func TestParseForm(t *testing.T) {
	t.Run("valid data", func(t *testing.T) {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		assert.NoError(t, writer.WriteField("test_field", "test_value"))
		assert.NoError(t, writer.Close())

		req, _ := http.NewRequest("POST", "localhost", body)
		req.Header.Set("Content-Type", "multipart/form-data; boundary="+writer.Boundary())

		form, err := parseForm(req, body.Bytes())
		assert.NoError(t, err)
		assert.Equal(t, "test_value", form.Value["test_field"][0])
	})

	t.Run("invalid data", func(t *testing.T) {
		req, _ := http.NewRequest("POST", "localhost", nil)
		req.Header.Set("Content-Type", "application/json")

		form, err := parseForm(req, nil)
		assert.Error(t, err)
		assert.Nil(t, form)
	})
}

func TestGetAppNameAndTags(t *testing.T) {
	t.Run("valid data", func(t *testing.T) {
		validUrl, err := http.NewRequest(http.MethodPost, "http://localhost/pyroscope/ingest?name=profiling-test%7Bcustom1%3D123456%2CserviceName%3Dmy-profiling-proj%7D&units=samples&aggregationType=sum&sampleRate=100&from=1708585375&until=1708585385&spyName=javaspy&format=jfr", &bytes.Buffer{})
		assert.NoError(t, err)
		appName, tags := getAppNameAndTags(validUrl)
		assert.Equal(t, "profiling-test", appName)
		assert.Equal(t, map[string]string{"custom1": "123456", "serviceName": "my-profiling-proj"}, tags)
	})

	t.Run("empty tags", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, "http://localhost/pyroscope/ingest?name=profiling-test%7B%7D&units=samples&aggregationType=sum&sampleRate=100&from=1708585375&until=1708585385&spyName=javaspy&format=jfr", &bytes.Buffer{})
		assert.NoError(t, err)
		appName, tags := getAppNameAndTags(req)
		assert.Equal(t, "profiling-test", appName)
		assert.Empty(t, tags)
	})

	t.Run("empty tags + empty app", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, "http://localhost/pyroscope/ingest?units=samples&aggregationType=sum&sampleRate=100&from=1708585375&until=1708585385&spyName=javaspy&format=jfr", &bytes.Buffer{})
		assert.NoError(t, err)
		appName, tags := getAppNameAndTags(req)
		assert.Empty(t, appName)
		assert.Empty(t, tags)
	})

	t.Run("invalid tags", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, "http://localhost/pyroscope/ingest?name=profiling-test%7B%7D%7D%7D&units=samples&aggregationType=sum&sampleRate=100&from=1708585375&until=1708585385&spyName=javaspy&format=jfr", &bytes.Buffer{})
		assert.NoError(t, err)
		appName, tags := getAppNameAndTags(req)
		assert.Equal(t, "profiling-test", appName)
		assert.Empty(t, tags)
	})
}
