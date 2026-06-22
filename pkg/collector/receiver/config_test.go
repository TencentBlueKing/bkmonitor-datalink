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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/throttle"
)

func TestLoadConfig(t *testing.T) {
	content := `
apm:
  patterns:
    - "../example/fixtures/subconfig.yml"
`
	config, err := confengine.LoadConfigContent(content)
	assert.NoError(t, err)

	subConfigs := LoadConfigFrom(config)

	subConfig, ok := subConfigs["token1"]
	assert.True(t, ok)
	assert.Equal(t, SkywalkingConfig{
		Sn: "my-test-sn",
		Rules: []SkywalkingRule{
			{
				Type:    "Http",
				Enabled: true,
				Target:  "cookie",
				Field:   "language",
			},
			{
				Type:    "Http",
				Enabled: true,
				Target:  "header",
				Field:   "Accept",
			},
			{
				Type:    "Http",
				Enabled: true,
				Target:  "query_parameter",
				Field:   "from",
			},
		},
	}, subConfig)
}

func TestLoadThrottleConfig(t *testing.T) {
	content := `
receiver:
  throttle:
    enabled: true
    sample_interval: 250ms
    signal:
      cpu_slow_beta: 0.95
      cpu_fast_beta: 0.7
      fallback_cores: 1.5
    thresholds:
      cpu_enter: 0.8
      cpu_exit: 0.7
      cpu_hard: 0.9
      mem_enter: 0.85
      mem_exit: 0.78
      mem_hard: 0.92
      breach_n: 2
    rules:
      default: {drop_min: 0.1, drop_max: 0.8}
      metrics: {enabled: false}
`
	config, err := confengine.LoadConfigContent(content)
	assert.NoError(t, err)

	var receiverConfig Config
	assert.NoError(t, config.UnpackChild("receiver", &receiverConfig))
	assert.True(t, receiverConfig.Throttle.Enabled)
	assert.Equal(t, 250*time.Millisecond, receiverConfig.Throttle.SampleInterval)
	assert.Equal(t, 0.95, receiverConfig.Throttle.Signal.CPUSlowBeta)
	assert.Equal(t, 1.5, receiverConfig.Throttle.Signal.FallbackCores)
	assert.Equal(t, 0.85, receiverConfig.Throttle.Thresholds.MemEnter)
	assert.Equal(t, 0.78, receiverConfig.Throttle.Thresholds.MemExit)
	assert.Equal(t, 2, receiverConfig.Throttle.Thresholds.BreachN)
	assert.NotNil(t, receiverConfig.Throttle.Rules[define.RecordMetrics.S()].Enabled)
	assert.False(t, *receiverConfig.Throttle.Rules[define.RecordMetrics.S()].Enabled)
	assert.NotNil(t, receiverConfig.Throttle.Rules["default"].DropMin)
	assert.Equal(t, 0.1, *receiverConfig.Throttle.Rules["default"].DropMin)
}

func TestNewWithThrottleDisabledKeepsMiddlewareAndClearsGlobalThrottle(t *testing.T) {
	throttle.Stop()
	defer throttle.Stop()
	assert.NoError(t, throttle.Init(throttle.Config{Enabled: true}))
	enabledManager := throttle.GlobalManager()

	content := `
receiver:
  http_server:
    middlewares:
      - "logging"
      - "throttle"
      - "maxconns;maxConnectionsRatio=2"
  admin_server:
    middlewares:
      - "throttle"
  grpc_server:
    middlewares:
      - "throttle"
      - "maxbytes;maxRequestBytes=1024"
  throttle:
    enabled: false
`
	config, err := confengine.LoadConfigContent(content)
	assert.NoError(t, err)

	r, err := New(config)
	assert.NoError(t, err)

	assert.Equal(t, []string{"logging", "throttle", "maxconns;maxConnectionsRatio=2"}, r.config.RecvServer.Middlewares)
	assert.Equal(t, []string{"throttle"}, r.config.AdminServer.Middlewares)
	assert.Equal(t, []string{"throttle", "maxbytes;maxRequestBytes=1024"}, r.config.GrpcServer.Middlewares)
	assert.NotSame(t, enabledManager, throttle.GlobalManager())
}
