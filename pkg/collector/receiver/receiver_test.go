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
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
)

func TestReceiver(t *testing.T) {
	const configContent = `
  receiver:
    disabled: false
    # Http Server Config
    http_server:
      # 是否启动 Http 服务
      # default: false
      enabled: true
      # 服务监听端点
      # default: ""
      endpoint: "localhost:0"
      # 服务中间件，目前支持：logging/cors/content_decompressor
      middlewares:
        - "logging"
        - "cors"
        - "content_decompressor"

    # Grpc Server Config
    grpc_server:
      # 是否启动 Grpc 服务
      # default: false
      enabled: true
      # 传输协议，目前支持 tcp
      # default: ""
      transport: "tcp"
      # 服务监听端点
      # default: ""
      endpoint: "localhost:0"

    components:
      jaeger:
        enabled: true
      otlp:
        enabled: true
      pushgateway:
        enabled: true
      zipkin:
        enabled: false
`

	config, err := confengine.LoadConfigContent(configContent)
	assert.NoError(t, err)

	r, err := New(config)
	assert.NoError(t, err)

	r.ready()
	assert.NoError(t, r.Start())
	assert.NoError(t, r.Stop())
	RecordHandleMetrics(DefaultMetricMonitor, define.Token{}, define.RequestHttp, define.RecordMetrics, 0, time.Now())
}

func TestPublisher(t *testing.T) {
	pub := Publisher{}
	wg := sync.WaitGroup{}
	wg.Add(2)
	go func() {
		defer wg.Done()
		pub.Publish(&define.Record{})
	}()

	go func() {
		defer wg.Done()
		<-Records()
	}()
	wg.Wait()
}

func TestRegisterDuplicateRoutes(t *testing.T) {
	assert.Panics(t, func() {
		RegisterHttpRoute("test", []RouteWithFunc{
			{
				Method:       http.MethodGet,
				RelativePath: "/route1",
			},
			{
				Method:       http.MethodGet,
				RelativePath: "/route1",
			},
		})
	})
}

func TestValidatePreCheckProcessors(t *testing.T) {
	t.Run("nil pipeline getter", func(t *testing.T) {
		code, p, err := validatePreCheckProcessors(nil, nil)
		assert.Equal(t, define.StatusCodeOK, code)
		assert.Equal(t, "", p)
		assert.NoError(t, err)
	})

	t.Run("noop pipeline getter", func(t *testing.T) {
		r := &define.Record{RecordType: define.RecordTraces}
		code, p, err := validatePreCheckProcessors(r, testkits.NewNoopPipeline())
		assert.Equal(t, define.StatusBadRequest, code)
		assert.Equal(t, "", p)
		assert.Error(t, err)
	})
}
