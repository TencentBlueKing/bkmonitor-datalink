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
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
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
	mainConf := processor.MustLoadConfigs(content)[0].Config

	customContent := `
processor:
  - name: "token_checker/fixed"
    config:
      type: "fixed"
      fixed_token: "token1"
      resource_key: " bk.data.token, bk.data.another.token "
      traces_dataid: 1009
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
	factory := obj.(*tokenChecker)
	assert.NoError(t, err)
	assert.Equal(t, mainConf, factory.MainConfig())

	var c1 Config
	assert.NoError(t, mapstructure.Decode(mainConf, &c1))
	(&c1).Clean()
	actualC1 := factory.configs.GetGlobal().(Config)
	assert.Equal(t, c1, actualC1)
	assert.Equal(t, []string{"bk.data.token"}, actualC1.resourceKeys)

	var c2 Config
	assert.NoError(t, mapstructure.Decode(customConf, &c2))
	(&c2).Clean()
	actualC2 := factory.configs.GetByToken("token1").(Config)
	assert.Equal(t, c2, actualC2)
	assert.Equal(t, []string{"bk.data.token", "bk.data.another.token"}, actualC2.resourceKeys)

	assert.Equal(t, define.ProcessorTokenChecker, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.True(t, factory.IsPreCheck())

	decoder := factory.decoders.GetGlobal().(TokenDecoder)
	err = factory.processCommon(decoder, &define.Record{RecordType: define.RecordTraces})
	assert.NoError(t, err)

	factory.Reload(mainConf, nil)
	assert.Equal(t, mainConf, factory.MainConfig())
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
		ResourceKey: " bk.data.token, bk.data.another.token  ",
		Salt:        "bk",
		DecodedIv:   "bkbkbkbkbkbkbkbk",
		DecodedKey:  "81be7fc6-5476-4934-9417-6d4d593728db",
	}

	decoders := confengine.NewTierConfig()
	decoders.SetGlobal(NewTokenDecoder(config))

	configs := confengine.NewTierConfig()
	(&config).Clean()
	configs.SetGlobal(config)
	return tokenChecker{
		decoders: decoders,
		configs:  configs,
	}
}

func skipTokenChecker() tokenChecker {
	config := Config{
		Type:    "fixed",
		AppName: "skip",
	}

	decoders := confengine.NewTierConfig()
	decoders.SetGlobal(NewTokenDecoder(config))

	configs := confengine.NewTierConfig()
	configs.SetGlobal(config)
	return tokenChecker{
		decoders: decoders,
		configs:  configs,
	}
}

func TestTracesAes256Token(t *testing.T) {
	t.Run("Incorrect Token", func(t *testing.T) {
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
	})

	t.Run("No Token", func(t *testing.T) {
		checker := aes256TokenChecker()
		g := makeTracesGenerator(1, nil)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}

		_, err := checker.Process(&record)
		assert.Error(t, err)
		assert.Equal(t, define.ErrSkipEmptyRecord, errors.Cause(err))
	})

	t.Run("Skip", func(t *testing.T) {
		checker := skipTokenChecker()
		g := makeTracesGenerator(1, nil)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}

		_, err := checker.Process(&record)
		assert.NoError(t, err)
	})

	t.Run("Success Attributes", func(t *testing.T) {
		checker := aes256TokenChecker()
		resources := map[string]string{
			"bk.data.another.token": "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw==",
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
	})

	t.Run("Success Header", func(t *testing.T) {
		checker := aes256TokenChecker()
		g := makeTracesGenerator(1, nil)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
			Token:      define.Token{Original: "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw=="},
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
	})

	t.Run("Failed Header", func(t *testing.T) {
		checker := aes256TokenChecker()
		g := makeTracesGenerator(1, nil)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
			Token:      define.Token{Original: "tKm3H4Ran78rWl85HwzfRgw"},
		}

		_, err := checker.Process(&record)
		assert.Error(t, err)
	})
}

func TestMetricsAes256Token(t *testing.T) {
	t.Run("Incorrect Token", func(t *testing.T) {
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
	})

	t.Run("No Token", func(t *testing.T) {
		checker := aes256TokenChecker()
		g := makeMetricsGenerator(1, nil)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordMetrics,
			Data:       data,
		}

		_, err := checker.Process(&record)
		assert.Error(t, err)
		assert.Equal(t, define.ErrSkipEmptyRecord, errors.Cause(err))
	})

	t.Run("Skip", func(t *testing.T) {
		checker := skipTokenChecker()
		g := makeMetricsGenerator(1, nil)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordMetrics,
			Data:       data,
		}

		_, err := checker.Process(&record)
		assert.NoError(t, err)
	})

	t.Run("Success Attributes", func(t *testing.T) {
		checker := aes256TokenChecker()
		resources := map[string]string{
			"bk.data.another.token": "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw==",
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
	})

	t.Run("Success Header", func(t *testing.T) {
		checker := aes256TokenChecker()
		g := makeMetricsGenerator(1, nil)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordMetrics,
			Data:       data,
			Token:      define.Token{Original: "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw=="},
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
	})

	t.Run("Failed Header", func(t *testing.T) {
		checker := aes256TokenChecker()
		g := makeMetricsGenerator(1, nil)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordMetrics,
			Data:       data,
			Token:      define.Token{Original: "tKm3H4Ran78rWl85HwzfRgw"},
		}

		_, err := checker.Process(&record)
		assert.Error(t, err)
	})
}

func TestLogsAes256Token(t *testing.T) {
	t.Run("Incorrect Token", func(t *testing.T) {
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
	})

	t.Run("No Token", func(t *testing.T) {
		checker := aes256TokenChecker()
		g := makeLogsGenerator(1, nil)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       data,
		}

		_, err := checker.Process(&record)
		assert.Error(t, err)
		assert.Equal(t, define.ErrSkipEmptyRecord, errors.Cause(err))
	})

	t.Run("Skip", func(t *testing.T) {
		checker := skipTokenChecker()
		g := makeLogsGenerator(1, nil)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       data,
		}

		_, err := checker.Process(&record)
		assert.NoError(t, err)
	})

	t.Run("Success Attributes", func(t *testing.T) {
		checker := aes256TokenChecker()
		resources := map[string]string{
			"bk.data.another.token": "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw==",
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
	})

	t.Run("Success Header", func(t *testing.T) {
		checker := aes256TokenChecker()
		g := makeLogsGenerator(1, nil)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       data,
			Token:      define.Token{Original: "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw=="},
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
	})

	t.Run("Failed Header", func(t *testing.T) {
		checker := aes256TokenChecker()
		g := makeLogsGenerator(1, nil)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       data,
			Token:      define.Token{Original: "tKm3H4Ran78rWl85HwzfRgw"},
		}

		_, err := checker.Process(&record)
		assert.Error(t, err)
	})
}

func TestProxyToken(t *testing.T) {
	content := `
  processor:
    - name: "token_checker/proxy"
      config:
        type: "proxy"
        proxy_dataid: 1100001
        proxy_token: "1100001_accesstoken"
`

	t.Run("Empty Token", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		data := &define.ProxyData{
			Data:        1001,
			AccessToken: "none_exist",
		}
		_, err := factory.Process(&define.Record{
			RecordType: define.RecordProxy,
			Data:       data,
		})
		assert.Equal(t, "reject empty token", err.Error())
	})

	t.Run("Invalid Token", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		data := &define.ProxyData{
			Data:        1001,
			AccessToken: "none_exist",
		}
		_, err := factory.Process(&define.Record{
			RecordType: define.RecordProxy,
			Token: define.Token{
				ProxyDataId: 1001,
				Original:    "none_exist",
			},
			Data: data,
		})
		assert.Equal(t, "reject invalid token: 1001/none_exist", err.Error())
	})

	t.Run("Correct Token", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		data := &define.ProxyData{
			Data:        1100001,
			AccessToken: "1100001_accesstoken",
		}
		_, err := factory.Process(&define.Record{
			RecordType: define.RecordProxy,
			Token: define.Token{
				ProxyDataId: 1100001,
				Original:    "1100001_accesstoken",
			},
			Data: data,
		})
		assert.NoError(t, err)
	})
}

func TestFtaAes256Token(t *testing.T) {
	t.Run("New Token", func(t *testing.T) {
		checker := aes256TokenChecker()
		record := define.Record{
			RecordType: define.RecordFta,
			Token: define.Token{
				Original: "Ymtia2JrYmtia2JrYmtiaxJ3i4amfEBRpRly3svdCllhrOjDgm6IjwqqIVKwzKN5",
			},
			Data: &define.FtaData{
				Data: []map[string]any{
					{"test": "test"},
				},
				EventId:    "1",
				IngestTime: time.Now().Unix(),
			},
		}

		_, err := checker.Process(&record)
		assert.NoError(t, err)
		assert.Equal(t, "tencent_cloud", record.Token.AppName)
		assert.Equal(t, int32(1001), record.Token.MetricsDataId)
	})

	t.Run("Incorrect Token", func(t *testing.T) {
		checker := aes256TokenChecker()
		record := define.Record{
			RecordType: define.RecordFta,
			Token: define.Token{
				Original: "12345",
			},
			Data: &define.FtaData{
				Data: []map[string]any{
					{"test": "test"},
				},
				EventId:    "1",
				IngestTime: time.Now().Unix(),
			},
		}

		_, err := checker.Process(&record)
		assert.True(t, strings.Contains(err.Error(), "failed to decode token"))
	})

	t.Run("Empty PluginID", func(t *testing.T) {
		checker := aes256TokenChecker()
		record := define.Record{
			RecordType: define.RecordFta,
			Token: define.Token{
				Original: "Ymtia2JrYmtia2JrYmtia/r4wM8mjJnSo8oBqbclwaCY2AaNBAvhq1T48ZO09PSe",
			},
			Data: &define.FtaData{
				Data: []map[string]any{
					{"test": "test"},
				},
				EventId:    "1",
				IngestTime: time.Now().Unix(),
			},
		}

		_, err := checker.Process(&record)
		assert.Equal(t, "reject invalid pluginId", err.Error())
	})

	t.Run("Empty DataID", func(t *testing.T) {
		checker := aes256TokenChecker()
		record := define.Record{
			RecordType: define.RecordFta,
			Token: define.Token{
				Original: "Ymtia2JrYmtia2JrYmtia5GdDXVAdxBFaOaaHF6kHUNG/yhSoPsPwAr1WfIhU8gc",
			},
			Data: &define.FtaData{
				Data: []map[string]any{
					{"test": "test"},
				},
				EventId:    "1",
				IngestTime: time.Now().Unix(),
			},
		}

		_, err := checker.Process(&record)
		assert.Equal(t, "reject invalid dataId", err.Error())
	})
}

func TestProfilesAes256Token(t *testing.T) {
	t.Run("Incorrect Token", func(t *testing.T) {
		checker := aes256TokenChecker()
		record := define.Record{
			RecordType: define.RecordProfiles,
			Token:      define.Token{Original: "Ymtia2JrYmtia2JrYmtiaxaNWo5XpK+8v5tQShWS+uJ1J7pzneLcmhLMc+A/9yKHx"},
		}

		_, err := checker.Process(&record)
		assert.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), "illegal base64 data at input byte 64"))
	})

	t.Run("No Token", func(t *testing.T) {
		checker := aes256TokenChecker()
		record := define.Record{
			RecordType: define.RecordProfiles,
		}

		_, err := checker.Process(&record)
		assert.Error(t, err)
	})

	t.Run("Skip", func(t *testing.T) {
		checker := skipTokenChecker()
		record := define.Record{
			RecordType: define.RecordProfiles,
		}

		_, err := checker.Process(&record)
		assert.NoError(t, err)
	})

	t.Run("Success", func(t *testing.T) {
		checker := aes256TokenChecker()
		record := define.Record{
			RecordType: define.RecordProfiles,
			Token:      define.Token{Original: "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw=="},
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
	})
}
