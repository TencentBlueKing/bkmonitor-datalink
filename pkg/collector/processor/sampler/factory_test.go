// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sampler

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/sampler/evaluator"
)

func TestFactory(t *testing.T) {
	content := `
processor:
  - name: "sampler/random"
    config:
      type: "random"
      sampling_percentage: 100
`
	mainConf := processor.MustLoadConfigs(content)[0].Config

	customContent := `
processor:
  - name: "sampler/random"
    config:
      type: "random"
      sampling_percentage: 80
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
	factory := obj.(*sampler)
	assert.NoError(t, err)
	assert.Equal(t, mainConf, factory.MainConfig())

	var c evaluator.Config
	assert.NoError(t, mapstructure.Decode(mainConf, &c))

	assert.Equal(t, define.ProcessorSampler, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.False(t, factory.IsPreCheck())

	factory.Reload(mainConf, nil)
	assert.Equal(t, mainConf, factory.MainConfig())
	factory.Clean()
}

func TestNoopFactory(t *testing.T) {
	content := `
processor:
  - name: "sampler/noop"
    config:
`
	factory := processor.MustCreateFactory(content, NewFactory)

	_, err := factory.Process(&define.Record{})
	assert.NoError(t, err)
}
