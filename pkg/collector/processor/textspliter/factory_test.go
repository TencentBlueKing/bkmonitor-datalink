// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package textspliter

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

func TestFactory(t *testing.T) {
	content := `
processor:
  - name: "text_spliter/newline"
    config:
      separator: "\n"
`
	mainConf := processor.MustLoadConfigs(content)[0].Config

	customContent := `
processor:
  - name: "text_spliter/comma"
    config:
      separator: ","
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
	factory := obj.(*textSpliter)
	assert.NoError(t, err)
	assert.Equal(t, mainConf, factory.MainConfig())

	mainConfig := factory.configs.GetGlobal().(Config)
	assert.Equal(t, "\n", mainConfig.Separator)

	customConfig := factory.configs.GetByToken("token1").(Config)
	assert.Equal(t, ",", customConfig.Separator)

	assert.Equal(t, define.ProcessorTextSpliter, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.False(t, factory.IsPreCheck())

	factory.Reload(mainConf, nil)
	assert.Equal(t, mainConf, factory.MainConfig())
}

func TestTextSpliter(t *testing.T) {
	content := `
processor:
    - name: "resource_filter/newline"
      config:
        separator: "\n"
`

	txt := `log
spliter
foobar
zzz`
	factory := processor.MustCreateFactory(content, NewFactory)
	record := define.Record{
		RecordType: define.RecordLogPush,
		Data: &define.LogPushData{
			Data: []string{txt},
			Labels: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
		},
	}

	_, err := factory.Process(&record)
	assert.NoError(t, err)

	expected := []string{
		"log",
		"spliter",
		"foobar",
		"zzz",
	}
	assert.Equal(t, expected, record.Data.(*define.LogPushData).Data)
}
