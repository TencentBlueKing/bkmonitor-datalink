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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
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

	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*probeFilter)
	assert.NoError(t, err)
	assert.Equal(t, psc[0].Config, factory.MainConfig())

	var c Config
	err = mapstructure.Decode(psc[0].Config, &c)
	assert.NoError(t, err)
	c.AddAttrs[0].Rules[0].Filters[0].Field = "service.name"
	c.AddAttrs[0].Rules[0].Filters[1].Field = "api_name"
	assert.Equal(t, c, factory.configs.Get("", "", "").(Config))

	assert.Equal(t, define.ProcessorProbeFilter, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.False(t, factory.IsPreCheck())
}

func makeTracesAttributesGenerator(n int, attrs map[string]string) *generator.TracesGenerator {
	opts := define.TracesOptions{SpanKind: n}
	opts.SpanCount = 1
	opts.Attributes = attrs
	opts.Resources = map[string]string{
		"service.name": "account",
	}
	return generator.NewTracesGenerator(opts)
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
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*probeFilter)
	assert.NoError(t, err)

	m := map[string]string{
		"http.headers":   "Accept=[Application/json]",
		"http.params":    "from=[actor] to=[order]",
		"sw8.span_layer": "Http",
	}
	g := makeTracesAttributesGenerator(int(ptrace.SpanKindUnspecified), m)
	data := g.Generate()

	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}
	_, err = factory.Process(&record)
	assert.NoError(t, err)
	span := record.Data.(ptrace.Traces).ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	attr := span.Attributes()
	v, ok := attr.Get("custom_tag.Accept")
	assert.True(t, ok)
	assert.Equal(t, "Application/json", v.StringVal())
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
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*probeFilter)
	assert.NoError(t, err)

	m := map[string]string{
		"http.headers":   "Cookie=[language=ZH-TEST]",
		"sw8.span_layer": "Http",
		"api_name":       "TestApiName",
	}
	g := makeTracesAttributesGenerator(int(ptrace.SpanKindUnspecified), m)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}

	_, err = factory.Process(&record)
	assert.NoError(t, err)
	span := record.Data.(ptrace.Traces).ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	attr := span.Attributes()
	v, ok := attr.Get("custom_tag.language")
	assert.True(t, ok)
	assert.Equal(t, "ZH-TEST", v.StringVal())
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
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*probeFilter)
	assert.NoError(t, err)

	m := map[string]string{
		"http.params":    "from=[TestFrom] to=[TestTo]",
		"sw8.span_layer": "Http",
		"api_name":       "TestApiName",
	}

	g := makeTracesAttributesGenerator(int(ptrace.SpanKindUnspecified), m)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}

	_, err = factory.Process(&record)
	assert.NoError(t, err)
	span := record.Data.(ptrace.Traces).ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	attr := span.Attributes()
	v, ok := attr.Get("custom_tag.from")
	assert.True(t, ok)
	assert.Equal(t, "TestFrom", v.StringVal())
}
