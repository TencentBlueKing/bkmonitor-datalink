// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package proxy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
)

func TestV2Push(t *testing.T) {
	content := `
proxy:
  disabled: false
`
	config := confengine.MustLoadConfigContent(content)
	proxy, err := newProxy(config)
	assert.NoError(t, err)

	body := `
{
    "data_id": 1100002,
    "access_token": "1100002_accesstoken",
    "data": [{
        "metrics": {
            "cpu_load": 10
        },
        "target": "127.0.0.1",
        "dimension": {
            "module": "db",
            "location": "guangdong"
        }
    }]
}
`
	proxy.Validator = pipeline.Validator{
		Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		},
	}

	rw := httptest.NewRecorder()
	buf := bytes.NewBufferString(body)
	req := httptest.NewRequest(http.MethodPost, routeV2Push, buf)
	proxy.V2PushRoute(rw, req)
	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, rw.Body.Bytes(), []byte(`{"code":"200","result":"true","message":""}`))
}

func TestV2EmptyPush(t *testing.T) {
	content := `
proxy:
  disabled: false
`
	config := confengine.MustLoadConfigContent(content)
	proxy, err := newProxy(config)
	assert.NoError(t, err)
	proxy.Validator = pipeline.Validator{
		Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		},
	}

	rw := httptest.NewRecorder()
	buf := bytes.NewBufferString("")
	req := httptest.NewRequest(http.MethodPost, routeV2Push, buf)
	proxy.V2PushRoute(rw, req)
	assert.True(t, strings.Contains(rw.Body.String(), "empty request body not allowed"))
}

func TestV2InvalidJsonPush(t *testing.T) {
	content := `
proxy:
  disabled: false
`
	config := confengine.MustLoadConfigContent(content)
	proxy, err := newProxy(config)
	assert.NoError(t, err)
	proxy.Validator = pipeline.Validator{
		Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		},
	}

	rw := httptest.NewRecorder()
	buf := bytes.NewBufferString("{-}")
	req := httptest.NewRequest(http.MethodPost, routeV2Push, buf)
	proxy.V2PushRoute(rw, req)
	assert.Equal(t, http.StatusBadRequest, rw.Code)
}

func TestV2PreCheckFailed(t *testing.T) {
	content := `
proxy:
  disabled: false
`
	config := confengine.MustLoadConfigContent(content)
	proxy, err := newProxy(config)
	assert.NoError(t, err)
	proxy.Validator = pipeline.Validator{
		Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeUnauthorized, "", errors.New("MUST ERROR")
		},
	}

	rw := httptest.NewRecorder()
	buf := bytes.NewBufferString("{}")
	req := httptest.NewRequest(http.MethodPost, routeV2Push, buf)
	proxy.V2PushRoute(rw, req)
	assert.Equal(t, http.StatusUnauthorized, rw.Code)
}

func TestV2ReadFailed(t *testing.T) {
	content := `
proxy:
  disabled: false
`
	config := confengine.MustLoadConfigContent(content)
	proxy, err := newProxy(config)
	assert.NoError(t, err)
	proxy.Validator = pipeline.Validator{
		Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		},
	}

	rw := httptest.NewRecorder()
	buf := testkits.NewBrokenReader()
	req := httptest.NewRequest(http.MethodPost, routeV2Push, buf)
	proxy.V2PushRoute(rw, req)
	assert.Equal(t, http.StatusInternalServerError, rw.Code)
}
