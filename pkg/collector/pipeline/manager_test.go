// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/apdexcalculator"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/attributefilter"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/metricsfilter"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/ratelimiter"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/resourcefilter"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/sampler"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/servicediscover"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/tokenchecker"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/tracesderiver"
)

func TestParseProcessor(t *testing.T) {
	t.Run("Invalid processor", func(t *testing.T) {
		content := `
processorx:
    - name: ""
      config:
`
		conf := confengine.MustLoadConfigContent(content)
		_, err := parseProcessors("x", conf, nil)
		assert.Error(t, err)
	})

	t.Run("Empty processor name", func(t *testing.T) {
		content := `
processor:
    - name: ""
      config:
`
		conf := confengine.MustLoadConfigContent(content)
		_, err := parseProcessors("x", conf, nil)
		assert.NoError(t, err)
	})

	t.Run("Duplicated processor", func(t *testing.T) {
		content := `
processor:
    - name: "apdex_calculator/fixed"
      config:
    - name: "apdex_calculator/fixed"
      config:
`
		conf := confengine.MustLoadConfigContent(content)
		_, err := parseProcessors("x", conf, nil)
		assert.NoError(t, err)
	})

	t.Run("No exist processor", func(t *testing.T) {
		content := `
processor:
    - name: "whatever/fixed"
      config:
`
		conf := confengine.MustLoadConfigContent(content)
		_, err := parseProcessors("x", conf, nil)
		assert.NoError(t, err)
	})
}

func TestParsePipeline(t *testing.T) {
	t.Run("Invalid pipeline", func(t *testing.T) {
		content := `
pipelinex:
    - name: ""
      config:
`
		conf := confengine.MustLoadConfigContent(content)
		_, err := parsePipelines("x", conf, nil)
		assert.Error(t, err)
	})

	t.Run("Empty pipeline name", func(t *testing.T) {
		content := `
pipeline:
    - name: ""
      config:
`
		conf := confengine.MustLoadConfigContent(content)
		_, err := parsePipelines("x", conf, nil)
		assert.NoError(t, err)
	})

	t.Run("Unknown pipeline type", func(t *testing.T) {
		content := `
pipeline:
    - name: "metrics_pipeline/common"
      type: "undefined"
      processors:
        - "token_checker/aes256"
        - "rate_limiter/token_bucket"
`
		conf := confengine.MustLoadConfigContent(content)
		_, err := parsePipelines("x", conf, nil)
		assert.NoError(t, err)
	})

	t.Run("Validate failed", func(t *testing.T) {
		content := `
pipeline:
    - name: "metrics_pipeline/common"
      type: "metrics"
      processors:
        - "sampler/status_code"
        - "token_checker/aes256"
        - "rate_limiter/token_bucket"
`
		conf := confengine.MustLoadConfigContent(content)
		_, err := parsePipelines("x", conf, nil)
		assert.NoError(t, err)
	})
}

func TestParseReportV2Configs(t *testing.T) {
	t.Run("Invalid type", func(t *testing.T) {
		content := `
type: report_v2
`
		conf := confengine.MustLoadConfigContent(content)
		parseReportV2Configs([]*confengine.Config{conf})
	})
}

func TestSubConfigParseAndLoad(t *testing.T) {
	patterns := []string{"../example/fixtures/*.yml"}
	configs := parseProcessorSubConfigs(confengine.LoadConfigPatterns(patterns))

	processors := configs["apdex_calculator/fixed"]
	assert.Len(t, processors, 3)

	defaultProcessor := processors[0]
	assert.Equal(t, defaultProcessor, processor.SubConfigProcessor{
		Token: "token1",
		Type:  "default",
		Config: processor.Config{
			Name: "apdex_calculator/fixed",
			Config: map[string]any{
				"calculator": map[string]any{
					"apdex_status": "satisfied",
					"type":         "fixed",
				},
				"rules": []any{
					map[string]any{
						"kind":        "",
						"destination": "apdex_type_subconfig1",
						"metric_name": "bk_apm_duration",
					},
				},
			},
		},
	})

	serviceProcessor := processors[1]
	assert.Equal(t, serviceProcessor, processor.SubConfigProcessor{
		Token: "token1",
		Type:  "service",
		ID:    "Go-Tier-Name",
		Config: processor.Config{
			Name: "apdex_calculator/fixed",
			Config: map[string]any{
				"calculator": map[string]any{
					"apdex_status": "tolerating",
					"type":         "fixed",
				},
				"rules": []any{
					map[string]any{
						"kind":        "",
						"destination": "apdex_type_subconfig2",
						"metric_name": "bk_apm_duration",
					},
				},
			},
		},
	})

	instanceProcessor := processors[2]
	assert.Equal(t, instanceProcessor, processor.SubConfigProcessor{
		Token: "token1",
		Type:  "instance",
		ID:    "golang:Go-Tier-Name:MANDOCHEN-MB0:127.0.0.1:8004",
		Config: processor.Config{
			Name: "apdex_calculator/fixed",
			Config: map[string]any{
				"calculator": map[string]any{
					"apdex_status": "frustrated",
					"type":         "fixed",
				},
				"rules": []any{
					map[string]any{
						"kind":        "",
						"destination": "apdex_type_subconfig3",
						"metric_name": "bk_apm_duration",
					},
				},
			},
		},
	})
}

func TestReportV2ConfigParseAndLoad(t *testing.T) {
	patterns := []string{"../example/fixtures/report_v2*.yml"}
	configs := parseReportV2Configs(confengine.LoadConfigPatterns(patterns))

	processors := configs["token_checker/proxy"]
	assert.Len(t, processors, 2)

	defaultProcessor := processors[0]
	assert.Equal(t, defaultProcessor, processor.SubConfigProcessor{
		Token: "1100001_accesstoken",
		Type:  "default",
		Config: processor.Config{
			Name: "token_checker/proxy",
			Config: map[string]any{
				"proxy_dataid": uint64(1100001),
				"proxy_token":  "1100001_accesstoken",
				"type":         "proxy",
			},
		},
	})

	processors = configs["rate_limiter/token_bucket"]
	assert.Len(t, processors, 2)

	defaultProcessor = processors[0]
	assert.Equal(t, defaultProcessor, processor.SubConfigProcessor{
		Token: "1100001_accesstoken",
		Type:  "default",
		Config: processor.Config{
			Name: "rate_limiter/token_bucket",
			Config: map[string]any{
				"qps":   uint64(500),
				"burst": uint64(1000),
				"type":  "token_bucket",
			},
		},
	})
}

func TestReportV1ConfigParseAndLoad(t *testing.T) {
	patterns := []string{"../example/fixtures/report_v1*.yml"}
	configs := parseReportV1Configs(confengine.LoadConfigPatterns(patterns))

	processors := configs["token_checker/proxy"]
	assert.Len(t, processors, 4)

	defaultProcessor := processors[0]
	assert.Equal(t, defaultProcessor, processor.SubConfigProcessor{
		Token: "1100001_accesstoken",
		Type:  "default",
		Config: processor.Config{
			Name: "token_checker/proxy",
			Config: map[string]any{
				"proxy_dataid": uint64(1100001),
				"proxy_token":  "1100001_accesstoken",
				"type":         "proxy",
			},
		},
	})

	processors = configs["rate_limiter/token_bucket"]
	assert.Len(t, processors, 4)

	defaultProcessor = processors[0]
	assert.Equal(t, defaultProcessor, processor.SubConfigProcessor{
		Token: "1100001_accesstoken",
		Type:  "default",
		Config: processor.Config{
			Name: "rate_limiter/token_bucket",
			Config: map[string]any{
				"qps":   uint64(1000),
				"burst": uint64(1000),
				"type":  "token_bucket",
			},
		},
	})
}

func TestNewManager(t *testing.T) {
	config, err := confengine.LoadConfigPath("../example/fixtures/main.yml")
	assert.NoError(t, err)

	config, err = config.Child("bk-collector")
	assert.NoError(t, err)

	manager, err := New(config)
	assert.NoError(t, err)
	assert.NoError(t, manager.Reload(config))

	pl := manager.GetPipeline(define.RecordTraces)
	assert.Equal(t, pl.Name(), "traces_pipeline/common")
	assert.Equal(t, pl.AllProcessors(), []string{
		"token_checker/fixed",
		"resource_filter/instance_id",
		"attribute_filter/as_string",
		"traces_deriver/duration",
		"sampler/random",
	})

	t.Run("Processors", func(t *testing.T) {
		assert.Equal(t, len(manager.processors), 7)

		tokenChecker, ok := manager.processors["token_checker/fixed"]
		assert.True(t, ok)

		type TokenCheckerConfig struct {
			TracesDataId  int32 `mapstructure:"traces_dataid"`
			MetricsDataId int32 `mapstructure:"metrics_dataid"`
			LogsDataId    int32 `mapstructure:"logs_dataid"`
		}

		var tokenCheckerConfig TokenCheckerConfig
		err = mapstructure.Decode(tokenChecker.MainConfig(), &tokenCheckerConfig)
		assert.NoError(t, err)
		assert.Equal(t, TokenCheckerConfig{
			TracesDataId:  11000,
			MetricsDataId: 11001,
			LogsDataId:    11002,
		}, tokenCheckerConfig)

		sampler, ok := manager.processors["sampler/random"]
		assert.True(t, ok)

		type SamplerConfig struct {
			SamplingPercentage float64 `mapstructure:"sampling_percentage"`
		}

		var samplerConfig SamplerConfig
		err = mapstructure.Decode(sampler.MainConfig(), &samplerConfig)
		assert.NoError(t, err)
		assert.Equal(t, SamplerConfig{
			SamplingPercentage: 100,
		}, samplerConfig)
	})

	t.Run("Privileged", func(t *testing.T) {
		traceDeriver, ok := manager.processors["traces_deriver/max"]
		assert.True(t, ok)

		type OperationsConfig struct {
			Operations []struct {
				MaxSeriesGrowthRate int `config:"max_series_growth_rate" mapstructure:"max_series_growth_rate"`
			} `config:"operations" mapstructure:"operations"`
		}

		var operationsConfig OperationsConfig
		err = mapstructure.Decode(traceDeriver.MainConfig(), &operationsConfig)
		assert.NoError(t, err)
		assert.Equal(t, 100, operationsConfig.Operations[0].MaxSeriesGrowthRate)
	})

	t.Run("Pipelines", func(t *testing.T) {
		assert.Len(t, manager.pipelines, 5)
		assert.NotNil(t, manager.GetProcessor("token_checker/fixed"))
		assert.Nil(t, manager.GetProcessor("token_checker/not_exist"))

		tracesPipeline := manager.GetPipeline(define.RecordTraces)
		assert.Len(t, tracesPipeline.AllProcessors(), 5)
		assert.Len(t, tracesPipeline.PreCheckProcessors(), 1)
		assert.Len(t, tracesPipeline.SchedProcessors(), 4)

		pushGatewayPipeline := manager.GetPipeline(define.RecordPushGateway)
		assert.Equal(t, []string{"token_checker/fixed"}, pushGatewayPipeline.AllProcessors())
	})
}
