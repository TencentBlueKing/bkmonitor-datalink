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
	"go.uber.org/atomic"

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

func readContent() []byte {
	content, err := os.ReadFile("../../example/fixtures/remotewrite.bytes")
	if err != nil {
		panic(err)
	}
	return content
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
	t.Run("invalid body", func(t *testing.T) {
		buf := bytes.NewBufferString("{-}")
		req := httptest.NewRequest(http.MethodPut, localPromWriteURL, buf)

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		rw := httptest.NewRecorder()
		svc.Write(rw, req)
		assert.Equal(t, rw.Code, http.StatusBadRequest)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("precheck failed", func(t *testing.T) {
		buf := bytes.NewBuffer(readContent())
		req := httptest.NewRequest(http.MethodPut, localPromWriteURL, buf)

		svc, n := newSvc(define.StatusBadRequest, "", errors.New("MUST ERROR"))
		rw := httptest.NewRecorder()
		svc.Write(rw, req)
		assert.Equal(t, rw.Code, http.StatusBadRequest)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("report success", func(t *testing.T) {
		buf := bytes.NewBuffer(readContent())
		req := httptest.NewRequest(http.MethodPut, localPromWriteURL, buf)

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		rw := httptest.NewRecorder()
		svc.Write(rw, req)
		assert.Equal(t, rw.Code, http.StatusOK)
		assert.Equal(t, int64(1), n.Load())
	})
}
