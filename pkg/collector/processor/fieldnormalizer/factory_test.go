// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fieldnormalizer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

func TestFactory(t *testing.T) {
	content := `
processor:
  - name: "field_normalizer/common"
    config:
      fields:
        - kind: "SPAN_KIND_SERVER"
          predicate_key: "attributes.http.method"
          rules:
            - key: "attributes.net.peer.name"
              op: contact
              values:
                - "attributes.client.address"
                - "attributes.client.port"
`
	mainConf := processor.MustLoadConfigs(content)[0].Config

	customContent := `
processor:
  - name: "field_normalizer/common"
    config:
      fields:
        - kind: "SPAN_KIND_SERVER"
          predicate_key: "attributes.http.method"
          rules:
            - key: "attributes.net.peer.name"
              op: contact
              values:
                - "attributes.client.addressx"
                - "attributes.client.portx"
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
	factory := obj.(*fieldNormalizer)
	assert.NoError(t, err)
	assert.Equal(t, mainConf, factory.MainConfig())

	var c Config
	assert.NoError(t, mapstructure.Decode(mainConf, &c))

	assert.Equal(t, define.ProcessorFieldNormalizer, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.False(t, factory.IsPreCheck())

	_, err = factory.Process(&define.Record{
		RecordType: define.RecordTraces,
		Data:       ptrace.NewTraces(),
	})
	assert.NoError(t, err)

	factory.Reload(mainConf, nil)
	assert.Equal(t, mainConf, factory.MainConfig())
	factory.Clean()
}
