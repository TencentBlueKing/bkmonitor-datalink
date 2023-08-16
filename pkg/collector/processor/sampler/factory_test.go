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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
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
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*sampler)
	assert.NoError(t, err)
	assert.Equal(t, psc[0].Config, factory.MainConfig())

	var c evaluator.Config
	err = mapstructure.Decode(psc[0].Config, &c)
	assert.NoError(t, err)
	assert.Equal(t, c, factory.configs.Get("", "", "").(evaluator.Config))

	assert.Equal(t, define.ProcessorSampler, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.False(t, factory.IsPreCheck())
	factory.Clean()
}

func TestNoopFactory(t *testing.T) {
	content := `
processor:
  - name: "sampler/noop"
    config:
`
	config := confengine.MustLoadConfigContent(content)
	var psc []processor.ProcessorConfig
	_ = config.UnpackChild("processor", &psc)

	f, err := NewFactory(psc[0].Config, nil)
	assert.NoError(t, err)
	derived, err := f.Process(&define.Record{})
	assert.NoError(t, err)
	assert.Nil(t, derived)
}
