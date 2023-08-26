// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tracesderiver

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

func TestDimensionMatcher(t *testing.T) {
	config, err := confengine.LoadConfigPath("../../example/fixtures/main.yml")
	assert.NoError(t, err)

	var processorConfigs []processor.ProcessorConfig
	err = config.UnpackChild("bk-collector.processor", &processorConfigs)
	assert.NoError(t, err)

	var tracesDerivedConfig *processor.ProcessorConfig
	for _, pc := range processorConfigs {
		if pc.Name == "traces_deriver/duration" {
			tracesDerivedConfig = &pc
		}
	}
	assert.NotNil(t, tracesDerivedConfig)

	var c Config
	err = mapstructure.Decode(tracesDerivedConfig.Config, &c)
	assert.NoError(t, err)

	g := generator.NewTracesGenerator(define.TracesOptions{
		GeneratorOptions: define.GeneratorOptions{
			RandomAttributeKeys: []string{"http.method"},
			RandomResourceKeys:  []string{"service.name"},
		},
		SpanCount: 10,
	})
	traces := g.Generate()
	span := testkits.FirstSpan(traces)

	fetcher := NewSpanDimensionMatcher(NewConfigHandler(c))
	dim, ok := fetcher.Match("duration", span)
	assert.True(t, ok)
	assert.True(t, len(dim) >= 1)

	resources := fetcher.MatchResource(traces.ResourceSpans().At(0))
	_, ok = resources["service.name"]
	assert.True(t, ok)
}

func TestDimensionMatcherBackup(t *testing.T) {
	config, err := confengine.LoadConfigPath("../../example/fixtures/main.yml")
	assert.NoError(t, err)

	var processorConfigs []processor.ProcessorConfig
	err = config.UnpackChild("bk-collector.processor", &processorConfigs)
	assert.NoError(t, err)

	var tracesDerivedConfig *processor.ProcessorConfig
	for _, pc := range processorConfigs {
		if pc.Name == "traces_deriver/duration" {
			tracesDerivedConfig = &pc
		}
	}
	assert.NotNil(t, tracesDerivedConfig)

	var c Config
	err = mapstructure.Decode(tracesDerivedConfig.Config, &c)
	assert.NoError(t, err)

	g := generator.NewTracesGenerator(define.TracesOptions{
		GeneratorOptions: define.GeneratorOptions{
			RandomAttributeKeys: []string{"attribute.http.methodx"},
			RandomResourceKeys:  []string{"status.code1"},
		},
		SpanCount: 10,
		SpanKind:  5,
	})
	traces := g.Generate()
	span := testkits.FirstSpan(traces)

	fetcher := NewSpanDimensionMatcher(NewConfigHandler(c))
	dim, ok := fetcher.Match("duration", span)
	assert.True(t, ok)
	assert.True(t, len(dim) >= 1)
	_, ok = dim["status.code1"]
	assert.True(t, ok)
}
