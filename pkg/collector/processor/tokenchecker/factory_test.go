// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tokenchecker

import (
	"strings"
	"testing"

	"github.com/mitchellh/mapstructure"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
)

func TestFactory(t *testing.T) {
	content := `
processor:
  - name: "token_checker/fixed"
    config:
      type: "fixed"
      fixed_token: "token1"
      resource_key: "bk.data.token"
      traces_dataid: 1000
      metrics_dataid: 1001
      logs_dataid: 1002
`
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*tokenChecker)
	assert.NoError(t, err)
	assert.Equal(t, psc[0].Config, factory.MainConfig())

	var c Config
	err = mapstructure.Decode(psc[0].Config, &c)
	assert.NoError(t, err)

	assert.Equal(t, define.ProcessorTokenChecker, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.True(t, factory.IsPreCheck())

	decoder := factory.decoders.Get("", "", "").(TokenDecoder)
	err = factory.processCommon(decoder, &define.Record{
		RecordType: define.RecordTraces,
	})
	assert.NoError(t, err)
}

func makeTracesGenerator(n int, resources map[string]string) *generator.TracesGenerator {
	opts := define.TracesOptions{SpanCount: n}
	opts.Resources = resources
	return generator.NewTracesGenerator(opts)
}

func makeMetricsGenerator(n int, resources map[string]string) *generator.MetricsGenerator {
	opts := define.MetricsOptions{GaugeCount: n}
	opts.Resources = resources
	return generator.NewMetricsGenerator(opts)
}

func makeLogsGenerator(n int, resources map[string]string) *generator.LogsGenerator {
	opts := define.LogsOptions{LogCount: n, LogLength: 16}
	opts.Resources = resources
	return generator.NewLogsGenerator(opts)
}

func aes256TokenChecker() tokenChecker {
	config := Config{
		Type:        "aes256",
		ResourceKey: "bk.data.token",
		Salt:        "bk",
		DecodedIv:   "bkbkbkbkbkbkbkbk",
		DecodedKey:  "81be7fc6-5476-4934-9417-6d4d593728db",
	}

	decoders := confengine.NewTierConfig()
	decoders.SetGlobal(NewTokenDecoder(config))
	return tokenChecker{
		config:   config,
		decoders: decoders,
	}
}

func TestTracesAes256IncorrectToken(t *testing.T) {
	checker := aes256TokenChecker()
	resources := map[string]string{
		"bk.data.token": "Ymtia2JrYmtia2JrYmtiaxaNWo5XpK+8v5tQShWS+uJ1J7pzneLcmhLMc+A/9yKHx",
	}
	g := makeTracesGenerator(1, resources)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}

	_, err := checker.Process(&record)
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "illegal base64 data at input byte 64"))
}

func TestMetricsAes256IncorrectToken(t *testing.T) {
	checker := aes256TokenChecker()
	resources := map[string]string{
		"bk.data.token": "Ymtia2JrYmtia2JrYmtiaxaNWo5XpK+8v5tQShWS+uJ1J7pzneLcmhLMc+A/9yKHx",
	}
	g := makeMetricsGenerator(1, resources)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	_, err := checker.Process(&record)
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "illegal base64 data at input byte 64"))
}

func TestLogsAes256IncorrectToken(t *testing.T) {
	checker := aes256TokenChecker()
	resources := map[string]string{
		"bk.data.token": "Ymtia2JrYmtia2JrYmtiaxaNWo5XpK+8v5tQShWS+uJ1J7pzneLcmhLMc+A/9yKHx",
	}
	g := makeLogsGenerator(1, resources)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordLogs,
		Data:       data,
	}

	_, err := checker.Process(&record)
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "illegal base64 data at input byte 64"))
}

func TestTracesAes256CorrectToken(t *testing.T) {
	checker := aes256TokenChecker()
	resources := map[string]string{
		"bk.data.token": "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw==",
	}
	g := makeTracesGenerator(1, resources)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}

	_, err := checker.Process(&record)
	assert.NoError(t, err)
	assert.Equal(t, define.Token{
		Original:      "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw==",
		MetricsDataId: 1002,
		TracesDataId:  1001,
		LogsDataId:    1003,
		BizId:         2,
		AppName:       "oneapm-appname",
	}, record.Token)
}

func TestMetricsAes256CorrectToken(t *testing.T) {
	checker := aes256TokenChecker()
	resources := map[string]string{
		"bk.data.token": "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw==",
	}
	g := makeMetricsGenerator(1, resources)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	_, err := checker.Process(&record)
	assert.NoError(t, err)
	assert.Equal(t, define.Token{
		Original:      "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw==",
		MetricsDataId: 1002,
		TracesDataId:  1001,
		LogsDataId:    1003,
		BizId:         2,
		AppName:       "oneapm-appname",
	}, record.Token)
}

func TestLogsAes256CorrectToken(t *testing.T) {
	checker := aes256TokenChecker()
	resources := map[string]string{
		"bk.data.token": "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw==",
	}
	g := makeLogsGenerator(1, resources)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordLogs,
		Data:       data,
	}

	_, err := checker.Process(&record)
	assert.NoError(t, err)
	assert.Equal(t, define.Token{
		Original:      "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw==",
		MetricsDataId: 1002,
		TracesDataId:  1001,
		LogsDataId:    1003,
		BizId:         2,
		AppName:       "oneapm-appname",
	}, record.Token)
}
