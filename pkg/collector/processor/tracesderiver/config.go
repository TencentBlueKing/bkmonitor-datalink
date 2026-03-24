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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/fields"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstrings"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/tracesderiver/accumulator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Config struct {
	Operations []OperationConfig `config:"operations" mapstructure:"operations"`
}

type OperationConfig struct {
	Type                string       `config:"type" mapstructure:"type"`
	MetricName          string       `config:"metric_name" mapstructure:"metric_name"`
	Rules               []RuleConfig `config:"rules" mapstructure:"rules"`
	MaxSeries           int          `config:"max_series" mapstructure:"max_series"`
	GcInterval          string       `config:"gc_interval" mapstructure:"gc_interval"`
	Buckets             []float64    `config:"buckets" mapstructure:"buckets"`
	PublishInterval     string       `config:"publish_interval" mapstructure:"publish_interval"`
	MaxSeriesGrowthRate int          `config:"max_series_growth_rate" mapstructure:"max_series_growth_rate"`
}

type RuleConfig struct {
	Kind         string   `config:"kind" mapstructure:"kind"`
	PredicateKey string   `config:"predicate_key" mapstructure:"predicate_key"`
	Dimensions   []string `config:"dimensions" mapstructure:"dimensions"`
}

type TypeWithName struct {
	Type       string
	MetricName string
}

type ConfigHandler struct {
	types         []TypeWithName
	kinds         map[string][]RuleConfig // key:[type+kind]
	predicateKeys *mapstrings.MapStrings  // key:[type+kind]
	resourceKeys  *mapstrings.MapStrings  // key:[type]
	attributeKeys *mapstrings.MapStrings  // key:[type+kind+predicateKey]
	methodKeys    *mapstrings.MapStrings  // key:[type+kind+predicateKey]

	accumulatorConfig *accumulator.Config
	extractorConfig   *ExtractorConfig
}

// NewConfigHandler 创建并返回 ConfigHandler 实例 用于管理配置和提取内容
// 使用一些列 map 来存储配置字段是为了不在运行时产生高频的解析开销 降低内存消耗
// 另外 Map(~O(1)) 的性能要比 Range(~O(N)) 优异
func NewConfigHandler(config Config) *ConfigHandler {
	kinds := make(map[string][]RuleConfig)
	predicateKeys := mapstrings.New(mapstrings.OrderDesc)
	resourceKeys := mapstrings.New(mapstrings.OrderNone)
	attributeKeys := mapstrings.New(mapstrings.OrderNone)
	methodKeys := mapstrings.New(mapstrings.OrderNone)

	var types []TypeWithName
	var accumulatorConfig *accumulator.Config
	var extractorConfig *ExtractorConfig
	for i := 0; i < len(config.Operations); i++ {
		conf := config.Operations[i]
		// accumulator 类型单独处理
		switch conf.Type {
		case accumulator.TypeDelta,
			accumulator.TypeDeltaDuration,
			accumulator.TypeCount,
			accumulator.TypeMin,
			accumulator.TypeMax,
			accumulator.TypeSum,
			accumulator.TypeBucket:
			if conf.MetricName != "" {
				gcInterval, _ := time.ParseDuration(conf.GcInterval)
				publishInterval, _ := time.ParseDuration(conf.PublishInterval)
				accumulatorConfig = &accumulator.Config{
					MetricName:          conf.MetricName,
					MaxSeries:           conf.MaxSeries,
					GcInterval:          gcInterval,
					PublishInterval:     publishInterval,
					Buckets:             conf.Buckets,
					Type:                conf.Type,
					MaxSeriesGrowthRate: conf.MaxSeriesGrowthRate,
				}
				accumulatorConfig.Validate()
			}
		case ExtractorType:
			gcInterval, _ := time.ParseDuration(conf.GcInterval)
			extractorConfig = &ExtractorConfig{
				MaxSeries:  conf.MaxSeries,
				GcInterval: gcInterval,
			}
			extractorConfig.Validate()

		default:
			logger.Errorf("invalid extractor type: %s", conf.Type)
			continue
		}

		types = append(types, TypeWithName{
			Type:       conf.Type,
			MetricName: conf.MetricName,
		})

		for j := 0; j < len(conf.Rules); j++ {
			kind := conf.Rules[j]
			key := conf.Type + "/" + kind.Kind
			kinds[key] = append(kinds[key], kind)
			predicateKeys.Set(key, kind.PredicateKey)

			for k := 0; k < len(kind.Dimensions); k++ {
				dim := kind.Dimensions[k]
				id := conf.Type + "/" + kind.Kind + "/" + kind.PredicateKey

				ff, v := fields.DecodeFieldFrom(dim)
				switch ff {
				case fields.FieldFromResource:
					resourceKeys.Set(conf.Type, v)
				case fields.FieldFromAttributes:
					attributeKeys.Set(id, v)
				case fields.FieldFromMethod:
					methodKeys.Set(id, v)
				default:
				}
			}
		}
	}

	return &ConfigHandler{
		types:             types,
		predicateKeys:     predicateKeys,
		resourceKeys:      resourceKeys,
		attributeKeys:     attributeKeys,
		methodKeys:        methodKeys,
		kinds:             kinds,
		accumulatorConfig: accumulatorConfig,
		extractorConfig:   extractorConfig,
	}
}

type ExtractorConfig struct {
	MaxSeries  int
	GcInterval time.Duration
}

func (ec *ExtractorConfig) Validate() {
	if ec.MaxSeries <= 0 {
		ec.MaxSeries = 100000 // 100k
	}
	if ec.GcInterval <= 0 {
		ec.GcInterval = time.Hour
	}
}

func (ch *ConfigHandler) GetAccumulatorConfig() *accumulator.Config {
	return ch.accumulatorConfig
}

func (ch *ConfigHandler) GetExtractorConfig() *ExtractorConfig {
	return ch.extractorConfig
}

func (ch *ConfigHandler) GetTypes() []TypeWithName {
	return ch.types
}

func (ch *ConfigHandler) GetResourceKeys(t string) []string {
	return ch.resourceKeys.Get(t)
}

func (ch *ConfigHandler) GetPredicateKeys(t, kind string) []string {
	keys := ch.predicateKeys.Get(t + "/" + kind)

	// 使用兜底配置
	if len(keys) == 0 {
		keys = ch.predicateKeys.Get(t + "/")
	}
	return keys
}

func (ch *ConfigHandler) GetAttributes(t, kind, predicateKey string) []string {
	keys := ch.attributeKeys.Get(t + "/" + kind + "/" + predicateKey)

	// 使用兜底配置
	if len(keys) == 0 && predicateKey == "" {
		return ch.attributeKeys.Get(t + "//" + predicateKey)
	}
	return keys
}

func (ch *ConfigHandler) GetMethods(t, kind, predicateKey string) []string {
	keys := ch.methodKeys.Get(t + "/" + kind + "/" + predicateKey)

	// 使用兜底配置
	if len(keys) == 0 && predicateKey == "" {
		return ch.methodKeys.Get(t + "//" + predicateKey)
	}
	return keys
}
