// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pingserver

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

type mockDetector struct{}

func (mockDetector) Do() {}

func (mockDetector) Result() map[string]*Response {
	return map[string]*Response{
		"127.0.0.1": {
			Addr: &net.IPAddr{
				IP: net.ParseIP("127.0.0.1"),
			},
			MaxRtt:    time.Second * 2,
			MinRtt:    time.Second,
			TotalRtt:  time.Second * 3,
			RecvCount: 3,
		},
	}
}

func TestPingServer(t *testing.T) {
	content := `
pingserver:
  disabled: false
  auto_reload: true
  patterns:
    - "../example/fixtures/pingserver_sub*.yml"
`
	conf := confengine.MustLoadConfigContent(content)
	ps, err := New(conf)
	assert.NoError(t, err)

	ps.createDetector = func(addrs []*net.IPAddr, times int, timeout time.Duration) Detector {
		return mockDetector{}
	}

	assert.Len(t, ps.config.Sub.Addrs(), 2)

	reloadContent := `
pingserver:
  disabled: false
  auto_reload: true
  patterns:
    - "../example/fixtures/pingserver_reload*.bak"
`
	assert.NoError(t, ps.Start())

	r := <-Records()
	data := r.Data.(*define.PingserverData).Data
	metrics := data["metrics"].(map[string]float64)

	assert.Equal(t, map[string]float64{
		"avg_rtt":      1000,
		"loss_percent": 0,
		"min_rtt":      1000,
		"max_rtt":      2000,
	}, metrics)

	err = ps.Reload(confengine.MustLoadConfigContent(reloadContent))
	assert.NoError(t, err)
	assert.Len(t, ps.config.Sub.Addrs(), 1)

	time.Sleep(time.Second * 2)
	ps.Stop()
}

func TestDefaultDetector(t *testing.T) {
	content := `
pingserver:
  disabled: false
  auto_reload: true
  patterns:
    - "../example/fixtures/pingserver_sub*.yml"
`
	conf := confengine.MustLoadConfigContent(content)
	ps, err := New(conf)
	assert.NoError(t, err)

	assert.NoError(t, ps.Start())
	ps.Stop()
}
