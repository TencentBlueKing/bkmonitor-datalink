// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"time"
)

type GeneratorOptions struct {
	Enabled             bool
	Iteration           int
	Interval            time.Duration
	RandomAttributeKeys []string
	RandomResourceKeys  []string
	Attributes          map[string]string
	Resources           map[string]string
	DimensionsValueType string // 支持 int/float/bool/string
}

type TracesOptions struct {
	GeneratorOptions
	SpanCount  int
	SpanKind   int
	EventCount int
	LinkCount  int
}

type MetricsOptions struct {
	GeneratorOptions
	MetricName     string
	Value          *float64
	GaugeCount     int
	CounterCount   int
	HistogramCount int
	SummaryCount   int
}

type LogsOptions struct {
	GeneratorOptions
	LogName   string
	LogLength int
	LogCount  int
}
