// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluator

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

type Config struct {
	Type          string `config:"type" mapstructure:"type"`
	MaxSpan       int    `config:"max_span" mapstructure:"max_span"`
	StoragePolicy string `config:"storage_policy" mapstructure:"storage_policy"`

	// random evaluator
	SamplingPercentage float64 `config:"sampling_percentage" mapstructure:"sampling_percentage"`

	// status_code evaluator
	MaxDuration time.Duration `config:"max_duration" mapstructure:"max_duration"`
	StatusCode  []string      `config:"status_code" mapstructure:"status_code"`

	// drop evaluator
	// 目前 enabled 字段只对 drop evaluator 生效
	Enabled bool `config:"enabled" mapstructure:"enabled"`
}

const (
	evaluatorTypeAlways     = "always"
	evaluatorTypeDrop       = "drop"
	evaluatorTypeRandom     = "random"
	evaluatorTypeStatusCode = "status_code"
)

type Evaluator interface {
	Type() string
	Stop()
	Evaluate(record *define.Record) error
}

func New(c Config) Evaluator {
	switch c.Type {
	case evaluatorTypeRandom:
		return newRandomEvaluator(c)
	case evaluatorTypeStatusCode:
		return newStatusCodeEvaluator(c)
	case evaluatorTypeDrop:
		return newDropEvaluator(c)
	}
	return newAlwaysEvaluator() // evaluatorTypeAlways
}
