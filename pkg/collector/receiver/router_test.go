// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package receiver

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
)

func TestRegister(t *testing.T) {
	RegisterRecvGrpcRoute(nil)
	RegisterRecvHttpRoute("x", nil)
	RegisterReadyFunc("x", func() {})
}

func TestRoute(t *testing.T) {
	const configContent = `
  receiver:
    disabled: false
    admin_server:
      enabled: true
      endpoint: "localhost:59999"
    grpc_server:
      enabled: false
`

	config := confengine.MustLoadConfigContent(configContent)
	r, err := New(config)
	assert.NoError(t, err)

	go func() {
		assert.NoError(t, r.Start())
	}()

	time.Sleep(time.Second)

	tests := []struct {
		method string
		path   string
	}{
		{
			method: http.MethodGet,
			path:   "/metrics",
		},
		{
			method: http.MethodPost,
			path:   "/-/logger",
		},
		{
			method: http.MethodGet,
			path:   "/-/routes",
		},
	}

	for _, c := range tests {
		var resp *http.Response
		var err error

		url := "http://localhost:59999" + c.path
		switch c.method {
		case http.MethodGet:
			resp, err = http.Get(url)
		case http.MethodPost:
			resp, err = http.Post(url, "", bytes.NewBufferString(""))
		}
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, 200, resp.StatusCode)
	}

	assert.NoError(t, r.Stop())
}
