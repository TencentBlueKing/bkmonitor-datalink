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
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

const (
	localPromWriteURL = "http://localhost/prometheus/write"
)

func TestReady(t *testing.T) {
	assert.NotPanics(t, Ready)
}

func TestHttpInvalidBody(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("{-}")
	req := httptest.NewRequest(http.MethodPut, localPromWriteURL, buf)

	var n int
	svc := HttpService{
		receiver.Publisher{Func: func(record *define.Record) { n++ }},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
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

	req := httptest.NewRequest(http.MethodPut, localPromWriteURL, buf)

	var n int
	svc := HttpService{
		receiver.Publisher{Func: func(record *define.Record) { n++ }},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
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

	req := httptest.NewRequest(http.MethodPut, localPromWriteURL, buf)

	var n int
	svc := HttpService{
		receiver.Publisher{Func: func(record *define.Record) { n++ }},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		}},
	}
	rw := httptest.NewRecorder()
	svc.Write(rw, req)
	assert.Equal(t, rw.Code, http.StatusOK)
	assert.Equal(t, 1, n)
}
