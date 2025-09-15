// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package probefilter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

func TestFactory(t *testing.T) {
	content := `
processor:
    - name: "probe_filter/common"
      config:
        add_attributes:
          - rules:
              - type: "Http"
                enabled: true
                target: "cookie"
                field: "language"
                prefix: "custom_tag"
                filters:
                  - field: "resource.service.name"
                    value: "account"
                    type: "service"
                  - field: "attributes.api_name"
                    value: "POST:/account/pay"
                    type: "interface"
`
	mainConf := processor.MustLoadConfigs(content)[0].Config

	customContent := `
processor:
    - name: "probe_filter/common"
      config:
        add_attributes:
          - rules:
              - type: "Http"
                enabled: true
                target: "cookie"
                field: "language"
                prefix: "custom_tag"
                filters:
                  - field: "resource.service.name"
                    value: "account1"
                    type: "service"
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
	factory := obj.(*probeFilter)
	assert.NoError(t, err)
	assert.Equal(t, mainConf, factory.MainConfig())

	mainConfig := factory.configs.GetGlobal().(Config)
	assert.Len(t, mainConfig.AddAttrs[0].Rules[0].Filters, 2)

	customConfig := factory.configs.GetByToken("token1").(Config)
	assert.Len(t, customConfig.AddAttrs[0].Rules[0].Filters, 1)

	assert.Equal(t, define.ProcessorProbeFilter, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.False(t, factory.IsPreCheck())

	factory.Reload(mainConf, nil)
	assert.Equal(t, mainConf, factory.MainConfig())
}

func makeTracesAttrsRecord(n int, attrs map[string]string) ptrace.Traces {
	opts := define.TracesOptions{SpanKind: n}
	opts.SpanCount = 1
	opts.Attributes = attrs
	opts.Resources = map[string]string{
		"service.name": "account",
	}
	return generator.NewTracesGenerator(opts).Generate()
}

func TestAddAttrsActionWithService(t *testing.T) {
	content := `
processor:
    - name: "probe_filter/common"
      config:
        add_attributes:
          - rules:
              - type: "Http"
                enabled: true
                target: "header"
                field: "Accept"
                prefix: "custom_tag"
                filters:
                  - field: "resource.service.name"
                    value: "account"
                    type: "service"
`
	factory := processor.MustCreateFactory(content, NewFactory)

	m := map[string]string{
		"http.headers":   "Accept=[Application/json]",
		"http.params":    "from=[actor] to=[order]",
		"sw8.span_layer": "Http",
	}
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       makeTracesAttrsRecord(int(ptrace.SpanKindUnspecified), m),
	}

	testkits.MustProcess(t, factory, record)
	span := testkits.FirstSpan(record.Data.(ptrace.Traces))
	testkits.AssertAttrsStringKeyVal(t, span.Attributes(), "custom_tag.Accept", "Application/json")
}

func TestAddAttrsActionWithInterface(t *testing.T) {
	content := `
processor:
    - name: "probe_filter/common"
      config:
        add_attributes:
          - rules:
              - type: "Http"
                enabled: true
                target: "cookie"
                field: "language"
                prefix: "custom_tag"
                filters:
                  - field: "attributes.api_name"
                    value: "TestApiName"
                    type: "interface"
`
	factory := processor.MustCreateFactory(content, NewFactory)

	m := map[string]string{
		"http.headers":   "Cookie=[language=ZH-TEST]",
		"sw8.span_layer": "Http",
		"api_name":       "TestApiName",
	}
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       makeTracesAttrsRecord(int(ptrace.SpanKindUnspecified), m),
	}

	testkits.MustProcess(t, factory, record)
	span := testkits.FirstSpan(record.Data.(ptrace.Traces))
	testkits.AssertAttrsStringKeyVal(t, span.Attributes(), "custom_tag.language", "ZH-TEST")
}

func TestAddAttrsActionWithQueryParams(t *testing.T) {
	content := `
processor:
    - name: "probe_filter/common"
      config:
        add_attributes:
          - rules:
              - type: "Http"
                enabled: true
                target: "query_parameter"
                field: "from"
                prefix: "custom_tag"
                filters:
                  - field: "attributes.api_name"
                    value: "TestApiName"
                    type: "interface"
`
	factory := processor.MustCreateFactory(content, NewFactory)

	m := map[string]string{
		"http.params":    "from=[TestFrom] to=[TestTo]",
		"sw8.span_layer": "Http",
		"api_name":       "TestApiName",
	}

	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       makeTracesAttrsRecord(int(ptrace.SpanKindUnspecified), m),
	}

	testkits.MustProcess(t, factory, record)
	span := testkits.FirstSpan(record.Data.(ptrace.Traces))
	testkits.AssertAttrsStringKeyVal(t, span.Attributes(), "custom_tag.from", "TestFrom")
}
