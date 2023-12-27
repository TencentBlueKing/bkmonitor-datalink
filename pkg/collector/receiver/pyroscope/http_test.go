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

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

func TestHttpInvalidParams(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("{-}")

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
}

func TestHttpInvalidSpyName(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("{-}")

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
}

func TestHttpInvalidBody(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("{-}")

	req, err := http.NewRequest(http.MethodPost, "http://localhost/pyroscope/ingest?aggregationType=sum&from=1698053090&name=fuxi%7B%7D&sampleRate=100&spyName=jfr&units=samples&until=1698053100", buf)
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
}

func TestHttpValidBody(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	fw, err := writer.CreateFormFile("profile", "profile.pprof")
	fw.Write([]byte("any profiles"))
	writer.Close()

	req, err := http.NewRequest(http.MethodPost, "http://localhost/pyroscope/ingest?aggregationType=sum&from=1698053090&name=fuxi%7B%7D&sampleRate=100&spyName=gospy&units=samples&until=1698053100", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer token_instance")
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
	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, 1, n)
}
