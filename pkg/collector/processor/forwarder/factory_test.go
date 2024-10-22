// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package forwarder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pkg/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

func TestFactory(t *testing.T) {
	content := `
processor:
  - name: "forwarder/traces"
    config:
      resolver:
        type: static
        endpoints:
        - ":1001"
        - ":1002"
`
	psc := processor.MustLoadConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*forwarder)
	assert.NoError(t, err)
	assert.Equal(t, psc[0].Config, factory.MainConfig())

	var c Config
	err = mapstructure.Decode(psc[0].Config, &c)
	assert.NoError(t, err)

	assert.Equal(t, define.ProcessorForwarder, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.False(t, factory.IsPreCheck())

	factory.Reload(psc[0].Config, nil)
	assert.Equal(t, psc[0].Config, factory.MainConfig())
	factory.Clean()
}

func TestFactoryProcess(t *testing.T) {
	content := `
processor:
  - name: "forwarder/traces"
    config:
      resolver:
        type: static
        identifier: ":1001"
        endpoints:
        - ":1001"
`
	factory := processor.MustCreateFactory(content, NewFactory)

	_, err := factory.Process(&define.Record{
		RecordType:    define.RecordTraces,
		RequestType:   "",
		RequestClient: define.RequestClient{},
		Token:         define.Token{},
		Data:          ptrace.NewTraces(),
	})
	assert.Equal(t, define.ErrEndOfPipeline, err)
}
