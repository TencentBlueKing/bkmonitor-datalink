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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestProxy(t *testing.T) {
	content := `
proxy:
  disabled: false
  http:
    port: 60990
    host: localhost
    middlewares:
      logging
`
	config := confengine.MustLoadConfigContent(content)
	proxy, err := New(config)
	assert.NoError(t, err)
	assert.NoError(t, proxy.Start())
	time.Sleep(time.Millisecond * 100)
	assert.NoError(t, proxy.Stop())

	globalRecords.Push(&define.Record{})
	select {
	case <-Records():
	default:
	}
}

func TestFailedOnConsul(t *testing.T) {
	content := `
proxy:
  disabled: false
  http:
    port: 60991
    host: localhost
  consul:
    enabled: true
    address: localhost:1234
`
	config := confengine.MustLoadConfigContent(content)
	proxy, err := New(config)
	assert.NoError(t, err)
	assert.Error(t, proxy.Start())
}
