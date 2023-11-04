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
			Config: map[string]interface{}{
				"calculator": map[string]interface{}{
					"apdex_status": "satisfied",
					"type":         "fixed",
				},
				"rules": []interface{}{
					map[string]interface{}{
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
			Config: map[string]interface{}{
				"calculator": map[string]interface{}{
					"apdex_status": "tolerating",
					"type":         "fixed",
				},
				"rules": []interface{}{
					map[string]interface{}{
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
			Config: map[string]interface{}{
				"calculator": map[string]interface{}{
					"apdex_status": "frustrated",
					"type":         "fixed",
				},
				"rules": []interface{}{
					map[string]interface{}{
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
			Config: map[string]interface{}{
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
			Config: map[string]interface{}{
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
			Config: map[string]interface{}{
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
			Config: map[string]interface{}{
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

	// assert processors
	assert.Equal(t, len(manager.processors), 7)

	tokenChecker, ok := manager.processors["token_checker/fixed"]
	assert.True(t, ok)

	type T1 struct {
		TracesDataId  int32 `mapstructure:"traces_dataid"`
		MetricsDataId int32 `mapstructure:"metrics_dataid"`
		LogsDataId    int32 `mapstructure:"logs_dataid"`
	}

	var tokenCheckerConfig T1
	err = mapstructure.Decode(tokenChecker.MainConfig(), &tokenCheckerConfig)
	assert.NoError(t, err)

	t1 := T1{
		TracesDataId:  11000,
		MetricsDataId: 11001,
		LogsDataId:    11002,
	}
	assert.Equal(t, t1, tokenCheckerConfig)

	sampler, ok := manager.processors["sampler/random"]
	assert.True(t, ok)

	type T2 struct {
		SamplingPercentage float64 `mapstructure:"sampling_percentage"`
	}

	var samplerConfig T2
	err = mapstructure.Decode(sampler.MainConfig(), &samplerConfig)
	assert.NoError(t, err)

	t2 := T2{
		SamplingPercentage: 100,
	}
	assert.Equal(t, t2, samplerConfig)

	// assert pipelines
	assert.Len(t, manager.pipelines, 5)
	assert.NotNil(t, manager.GetProcessor("token_checker/fixed"))
	assert.Nil(t, manager.GetProcessor("token_checker/not_exist"))

	tracesPipeline := manager.GetPipeline(define.RecordTraces)
	assert.Len(t, tracesPipeline.AllProcessors(), 5)
	assert.Len(t, tracesPipeline.PreCheckProcessors(), 1)
	assert.Len(t, tracesPipeline.SchedProcessors(), 4)

	metricsDerived := manager.GetPipeline(define.RecordMetricsDerived)
	assert.Len(t, metricsDerived.AllProcessors(), 3)

	pushGatewayPipeline := manager.GetPipeline(define.RecordPushGateway)
	assert.Equal(t, []string{"token_checker/fixed"}, pushGatewayPipeline.AllProcessors())
}
