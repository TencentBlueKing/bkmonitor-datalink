// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package servicediscover

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

func TestFactory(t *testing.T) {
	content := `
processor:
  - name: "service_discover/common"
    config:
      rules:
        - service: "my-service"
          type: "http"
          match_type: "manual"
          predicate_key: "attributes.http.method"
          kind: "SPAN_KIND_CLIENT"
          match_key: "attributes.http.url"
          match_groups:
            - source: "service"
              destination: "peer.service"
          rule:
            params:
              - name: "version"
                operator: "eq"
                value: "v1"
              - name: "user"
                operator: "nq"
                value: "mando"
            host:
              value: "https://doc.weixin.qq.com"
              operator: eq
            path:
              value: "/api/v1/users"
              operator: nq
  
        - service: "None"
          type: "http"
          match_type: "auto"
          predicate_key: "attributes.http.method"
          kind: "SPAN_KIND_CLIENT"
          match_key: "attributes.http.url"
          match_groups:
            - source: "peer_service"
              destination: "peer.service"
            - source: "span_name"
              destination: "span_name"
          rule:
            regex: "https://(?P<peer_service>[^/]+)/(?P<span_name>\\w+)/.+"
`
	mainConf := processor.MustLoadConfigs(content)[0].Config

	customContent := `
processor:
  - name: "service_discover/common"
    config:
      rules:
        - service: "my-service"
          type: "http"
          match_type: "manual"
          predicate_key: "attributes.http.method"
          kind: "SPAN_KIND_CLIENT"
          match_key: "attributes.http.url"
          match_groups:
            - source: "service"
              destination: "peer.service"
          rule:
            path:
              value: "/api/v1/users"
              operator: eq
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
	factory := obj.(*serviceDiscover)
	assert.NoError(t, err)
	assert.Equal(t, mainConf, factory.MainConfig())

	c := &Config{}
	assert.NoError(t, mapstructure.Decode(mainConf, c))
	c.Setup()

	rules := []*Rule{
		{
			Type:         "http",
			Kind:         "SPAN_KIND_CLIENT",
			Service:      "my-service",
			MatchType:    "manual",
			MatchKey:     "attributes.http.url",
			PredicateKey: "attributes.http.method",
			MatchConfig: MatchConfig{
				Params: []RuleParam{
					{
						Name:     "version",
						Operator: "eq",
						Value:    "v1",
					},
					{
						Name:     "user",
						Operator: "nq",
						Value:    "mando",
					},
				},
				Host: RuleHost{
					Operator: "eq",
					Value:    "https://doc.weixin.qq.com",
				},
				Path: RulePath{
					Operator: "nq",
					Value:    "/api/v1/users",
				},
			},
			MatchGroups: []MatchGroup{
				{
					Source:      "service",
					Destination: "peer.service",
				},
			},
		},
		{
			Type:         "http",
			Kind:         "SPAN_KIND_CLIENT",
			Service:      "None",
			MatchType:    "auto",
			MatchKey:     "attributes.http.url",
			PredicateKey: "attributes.http.method",
			MatchConfig: MatchConfig{
				Regex: `https://(?P<peer_service>[^/]+)/(?P<span_name>\w+)/.+`,
			},
			MatchGroups: []MatchGroup{
				{
					Source:      "peer_service",
					Destination: "peer.service",
				},
				{
					Source:      "span_name",
					Destination: "span_name",
				},
			},
			mappings: map[string]string{
				"peer_service": "peer.service",
				"span_name":    "span_name",
			},
			re: regexp.MustCompile(`https://(?P<peer_service>[^/]+)/(?P<span_name>\w+)/.+`),
		},
	}

	ch := NewConfigHandler(c)
	assert.Equal(t, rules[0], ch.Get("SPAN_KIND_CLIENT")[0])
	assert.Equal(t, rules[1], ch.Get("SPAN_KIND_CLIENT")[1])

	assert.Equal(t, define.ProcessorServiceDiscover, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.False(t, factory.IsPreCheck())

	factory.Reload(mainConf, nil)
	assert.Equal(t, mainConf, factory.MainConfig())
}

func TestTraceManualMatched(t *testing.T) {
	content := `
processor:
  - name: "service_discover/common"
    config:
      rules:
        - service: "my-service"
          type: "http"
          match_type: "manual"
          predicate_key: "attributes.http.method"
          kind: "SPAN_KIND_CLIENT"
          match_key: "attributes.http.url"
          match_groups:
            - source: service
              destination: peer.service
            - source: path
              destination: span_name
          rule:
            params:
              - name: version
                operator: eq
                value: v1
              - name: user
                operator: nq
                value: mando
            host:
              operator: eq
              value: doc.weixin.qq.com
            path:
              operator: eq
              value: /api/v1/users
`
	factory := processor.MustCreateFactory(content, NewFactory)

	traces := generator.NewTracesGenerator(define.TracesOptions{
		GeneratorOptions: define.GeneratorOptions{
			Attributes: map[string]string{
				"http.method": "GET",
				"http.url":    "https://doc.weixin.qq.com/api/v1/users?version=v1&user=jacky",
			},
		},
		SpanCount: 2,
		SpanKind:  int(ptrace.SpanKindClient),
	})
	data := traces.Generate()

	record := &define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}
	_, err := factory.Process(record)
	assert.NoError(t, err)

	data = record.Data.(ptrace.Traces)
	foreach.Spans(data.ResourceSpans(), func(span ptrace.Span) {
		testkits.AssertAttrsFoundStringVal(t, span.Attributes(), "peer.service", "my-service")
		assert.Equal(t, "/api/v1/users", span.Name())
	})
}

func TestTracesAutoMatched(t *testing.T) {
	content := `
processor:
  - name: "service_discover/common"
    config:
      rules:
        - service: "None"
          type: "http"
          match_type: "auto"
          predicate_key: "attributes.http.method"
          kind: "SPAN_KIND_CLIENT"
          match_key: "attributes.http.url"
          match_groups:
            - source: peer_service
              destination: peer.service
            - source: span_name
              destination: span_name
          rule:
            regex: https://(?P<peer_service>[^/]+)/(?P<span_name>\w+)/.+
`
	factory := processor.MustCreateFactory(content, NewFactory)

	traces := generator.NewTracesGenerator(define.TracesOptions{
		GeneratorOptions: define.GeneratorOptions{
			Attributes: map[string]string{
				"http.method": "GET",
				"http.url":    "https://doc.weixin.qq.com/api/v1/users",
			},
		},
		SpanCount: 2,
		SpanKind:  int(ptrace.SpanKindClient),
	})
	data := traces.Generate()

	record := &define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}
	_, err := factory.Process(record)
	assert.NoError(t, err)

	data = record.Data.(ptrace.Traces)
	foreach.Spans(data.ResourceSpans(), func(span ptrace.Span) {
		testkits.AssertAttrsFoundStringVal(t, span.Attributes(), "peer.service", "doc.weixin.qq.com")
		assert.Equal(t, "api", span.Name())
	})
}

func TestTracesAutoMatchedWithoutSpanName(t *testing.T) {
	content := `
processor:
  - name: "service_discover/common"
    config:
      rules:
        - service: "None"
          type: "http"
          match_type: "auto"
          predicate_key: "attributes.http.method"
          kind: "SPAN_KIND_CLIENT"
          match_key: "attributes.http.url"
          match_groups:
            - source: "peer_service"
              destination: "peer.service"
          rule:
            regex: https://(?P<peer_service>[^/]+)/
`
	factory := processor.MustCreateFactory(content, NewFactory)

	traces := generator.NewTracesGenerator(define.TracesOptions{
		GeneratorOptions: define.GeneratorOptions{
			Attributes: map[string]string{
				"http.method": "GET",
				"http.url":    "https://doc.weixin.qq.com/api/v1/users",
			},
		},
		SpanCount: 2,
		SpanKind:  int(ptrace.SpanKindClient),
	})
	data := traces.Generate()

	record := &define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}
	_, err := factory.Process(record)
	assert.NoError(t, err)

	data = record.Data.(ptrace.Traces)
	foreach.Spans(data.ResourceSpans(), func(span ptrace.Span) {
		testkits.AssertAttrsFoundStringVal(t, span.Attributes(), "peer.service", "doc.weixin.qq.com")
	})
}
