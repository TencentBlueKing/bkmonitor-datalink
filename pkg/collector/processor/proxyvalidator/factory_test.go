// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package proxyvalidator

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

func TestFactory(t *testing.T) {
	content := `
processor:
    - name: "proxy_validator/common"
      config:
        type: time_series
        version: v2
        max_future_time_offset: 3600
`
	mainConf := processor.MustLoadConfigs(content)[0].Config

	customContent := `
processor:
    - name: "proxy_validator/common"
      config:
        type: time_series
        version: v2
        max_future_time_offset: 7200
`
	customConf := processor.MustLoadConfigs(customContent)[0].Config

	obj, err := NewFactory(mainConf, []processor.SubConfigProcessor{
		{
			Token: "token1",
			Type:  define.SubConfigFieldDefault,
			Config: processor.Config{
				Config: customConf,
			},
		},
	})
	factory := obj.(*proxyValidator)
	assert.NoError(t, err)
	assert.Equal(t, mainConf, factory.MainConfig())

	var c1 Config
	assert.NoError(t, mapstructure.Decode(mainConf, &c1))

	assert.Equal(t, define.ProcessorProxyValidator, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.True(t, factory.IsPreCheck())

	factory.Reload(mainConf, nil)
	assert.Equal(t, mainConf, factory.MainConfig())
}

func TestProcess(t *testing.T) {
	content := `
processor:
    - name: "proxy_validator/common"
      config:
        type: time_series
        version: v2
`
	factory := processor.MustCreateFactory(content, NewFactory)

	t.Run("Unsupported", func(t *testing.T) {
		_, err := factory.Process(&define.Record{})
		assert.True(t, strings.Contains(err.Error(), "unsupported"))
	})

	t.Run("Empty", func(t *testing.T) {
		_, err := factory.Process(&define.Record{
			RecordType: define.RecordProxy,
			Data:       &define.ProxyData{},
		})
		assert.Error(t, err)
	})
}
