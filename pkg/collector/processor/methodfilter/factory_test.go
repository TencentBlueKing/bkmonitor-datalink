// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package methodfilter

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

func TestFactory(t *testing.T) {
	content := `
processor:
  - name: "method_filter/drop_span"
    config:
      drop_span:
        rules:
          - predicate_key: "span_name"
            kind: "SPAN_KIND_SERVER"
            match:
              op: "reg"
              value: "GET:/benchmark/[^/]+"
`
	mainConf := processor.MustLoadConfigs(content)[0].Config

	customContent := `
processor:
  - name: "method_filter/drop_span"
    config:
      drop_span:
        rules:
          - predicate_key: "span_name"
            kind: "SPAN_KIND_CLIENT"
            match:
              op: "reg"
              value: "/benchmark/[^/]+"
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
	factory := obj.(*methodFilter)
	assert.NoError(t, err)
	assert.Equal(t, mainConf, factory.MainConfig())
	assert.Equal(t, customConf, factory.SubConfigs()[0].Config.Config)

	c := &Config{}
	assert.NoError(t, mapstructure.Decode(mainConf, c))

	rules := []*Rule{
		{
			Kind:         "SPAN_KIND_SERVER",
			PredicateKey: "span_name",
			MatchConfig: MatchConfig{
				Op:    "reg",
				Value: `GET:/benchmark/[^/]+`,
			},
		},
	}

	ch := NewConfigHandler(c)
	assert.Equal(t, rules[0], ch.Get("SPAN_KIND_SERVER")[0])

	assert.Equal(t, define.ProcessorMethodFilter, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.False(t, factory.IsPreCheck())

	factory.Reload(mainConf, nil)
	assert.Equal(t, mainConf, factory.MainConfig())
}

func TestDropSpanAction(t *testing.T) {
	content := `
processor:
  - name: "method_filter/drop_span"
    config:
      drop_span:
        rules:
          - predicate_key: "span_name"
            kind: "SPAN_KIND_INTERNAL,SPAN_KIND_SERVER,SPAN_KIND_CLIENT"
            match:
              op: "reg"
              value: "getAge"
          - predicate_key: "span_name"
            kind: "SPAN_KIND_INTERNAL,SPAN_KIND_SERVER"
            match:
              op: "reg"
              value: "queryAge"
`

	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)

		b, err := os.ReadFile("../../example/fixtures/traces2.json")
		assert.NoError(t, err)
		traces, err := generator.FromJsonToTraces(b)
		assert.NoError(t, err)
		assert.Equal(t, 15, traces.SpanCount())

		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       traces,
		}

		_, processErr := factory.Process(&record)
		assert.NoError(t, processErr)

		assert.Equal(t, 9, record.Data.(ptrace.Traces).SpanCount())
	})
}
