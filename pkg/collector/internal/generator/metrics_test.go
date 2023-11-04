// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package generator

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestMetrics(t *testing.T) {
	v := float64(10)
	g := NewMetricsGenerator(define.MetricsOptions{
		GeneratorOptions: define.GeneratorOptions{
			RandomAttributeKeys: []string{"attr1", "attr2"},
			RandomResourceKeys:  []string{"res1", "res2"},
			Resources:           map[string]string{"foo": "bar"},
			Attributes:          map[string]string{"hello": "mando"},
		},
		MetricName:     "testmetric",
		Value:          &v,
		GaugeCount:     1,
		CounterCount:   1,
		HistogramCount: 1,
		SummaryCount:   1,
	})

	data := g.Generate()
	assert.NotNil(t, data)
}
