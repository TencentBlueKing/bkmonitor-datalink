// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package remotewrite

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

func TestReady(t *testing.T) {
	assert.NotPanics(t, Ready)
}

func TestHttpInvalidBody(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("{-}")

	req, err := http.NewRequest(http.MethodPut, "http://localhost/prometheus/write", buf)
	assert.NoError(t, err)

	var n int
	svc := HttpService{
		receiver.Publisher{Func: func(record *define.Record) { n++ }},
		receiver.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		}},
	}
	rw := httptest.NewRecorder()
	svc.Write(rw, req)
	assert.Equal(t, rw.Code, http.StatusBadRequest)
	assert.Equal(t, 0, n)
}

func TestHttpPreCheckFailed(t *testing.T) {
	buf := &bytes.Buffer{}
	content, err := os.ReadFile("../../example/fixtures/remotewrite.bytes")
	assert.NoError(t, err)
	buf.Write(content)

	req, err := http.NewRequest(http.MethodPut, "http://localhost/prometheus/write", buf)
	assert.NoError(t, err)

	var n int
	svc := HttpService{
		receiver.Publisher{Func: func(record *define.Record) { n++ }},
		receiver.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusBadRequest, "", errors.New("MUST ERROR")
		}},
	}
	rw := httptest.NewRecorder()
	svc.Write(rw, req)
	assert.Equal(t, rw.Code, http.StatusBadRequest)
	assert.Equal(t, 0, n)
}

func TestHttpTokenAfterPreCheck(t *testing.T) {
	buf := &bytes.Buffer{}
	content, err := os.ReadFile("../../example/fixtures/remotewrite.bytes")
	assert.NoError(t, err)
	buf.Write(content)

	req, err := http.NewRequest(http.MethodPut, "http://localhost/prometheus/write", buf)
	assert.NoError(t, err)

	var n int
	svc := HttpService{
		receiver.Publisher{Func: func(record *define.Record) { n++ }},
		receiver.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		}},
	}
	rw := httptest.NewRecorder()
	svc.Write(rw, req)
	assert.Equal(t, rw.Code, http.StatusOK)
	assert.Equal(t, 1, n)
}
