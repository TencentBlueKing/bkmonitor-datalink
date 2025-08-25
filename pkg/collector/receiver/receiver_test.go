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
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
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

    # Tars Server Config
    tars_server:
      # 是否启动 Tars 服务
      # default: false
      enabled: true
      # 传输协议，目前支持 tcp
      # default: ""
      transport: "tcp"
      # 服务监听端点
      # default: ""
      endpoint: ":4319"

    components:
      jaeger:
        enabled: true
      otlp:
        enabled: true
      pushgateway:
        enabled: true
      zipkin:
        enabled: true
      remotewrite:
        enabled: true
      skywalking:
        enabled: true
      pyroscope:
        enabled: true
      fta:
        enabled: true
      beat:
        enabled: true
      tars:
        enabled: true
`

	config := confengine.MustLoadConfigContent(configContent)
	r, err := New(config)
	assert.NoError(t, err)

	componentsReady[define.SourceJaeger] = func(_ ComponentConfig) { t.Logf("%s ready", define.SourceJaeger) }
	componentsReady[define.SourceOtlp] = func(_ ComponentConfig) { t.Logf("%s ready", define.SourceOtlp) }
	componentsReady[define.SourcePushGateway] = func(_ ComponentConfig) { t.Logf("%s ready", define.SourcePushGateway) }
	componentsReady[define.SourceRemoteWrite] = func(_ ComponentConfig) { t.Logf("%s ready", define.SourceRemoteWrite) }
	componentsReady[define.SourceZipkin] = func(_ ComponentConfig) { t.Logf("%s ready", define.SourceZipkin) }
	componentsReady[define.SourceSkywalking] = func(_ ComponentConfig) { t.Logf("%s ready", define.SourceSkywalking) }
	componentsReady[define.SourcePyroscope] = func(_ ComponentConfig) { t.Logf("%s ready", define.SourcePyroscope) }
	componentsReady[define.SourceFta] = func(_ ComponentConfig) { t.Logf("%s ready", define.SourceFta) }
	componentsReady[define.SourceBeat] = func(_ ComponentConfig) { t.Logf("%s ready", define.SourceBeat) }
	componentsReady[define.SourceTars] = func(_ ComponentConfig) { t.Logf("%s ready", define.SourceTars) }

	r.ready()
	assert.NoError(t, r.Start())
	r.Reload(config)
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
		RegisterRecvHttpRoute("test", []RouteWithFunc{
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

func TestSkywalkingFetcher(t *testing.T) {
	t.Run("Define function", func(t *testing.T) {
		fetcher := SkywalkingConfigFetcher{
			Func: func(s string) SkywalkingConfig {
				return SkywalkingConfig{
					Sn: "sn1",
				}
			},
		}

		config := fetcher.Fetch("token1")
		assert.Equal(t, "sn1", config.Sn)
	})

	t.Run("Default", func(t *testing.T) {
		var fetcher SkywalkingConfigFetcher
		config := fetcher.Fetch("token1")
		assert.Equal(t, "", config.Sn)
	})
}

func TestWriteResponse(t *testing.T) {
	r := httptest.NewRecorder()
	WriteResponse(r, "application/json", 200, nil)
	assert.Equal(t, 200, r.Result().StatusCode)
}
