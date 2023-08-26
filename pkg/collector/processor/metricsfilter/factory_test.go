// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metricsfilter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
)

func TestFactory(t *testing.T) {
	content := `
processor:
  - name: "metrics_filter/drop"
    config:
      drop:
        metrics:
          - "runtime.go.mem.live_objects"
          - "none.exist.metric"
`
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*metricsFilter)
	assert.NoError(t, err)
	assert.Equal(t, psc[0].Config, factory.MainConfig())

	var c Config
	err = mapstructure.Decode(psc[0].Config, &c)
	assert.NoError(t, err)
	assert.Equal(t, c, factory.configs.Get("", "", "").(Config))

	assert.Equal(t, define.ProcessorMetricsFilter, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.False(t, factory.IsPreCheck())
}

func makeMetricsGenerator(n int) *generator.MetricsGenerator {
	opts := define.MetricsOptions{
		MetricName: "my_metrics",
		GaugeCount: n,
	}
	return generator.NewMetricsGenerator(opts)
}

func TestMetricsNoAction(t *testing.T) {
	g := makeMetricsGenerator(1)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	metrics := record.Data.(pmetric.Metrics)
	assert.Equal(t, 1, metrics.ResourceMetrics().Len())

	name := testkits.FirstMetric(metrics).Name()
	assert.Equal(t, "my_metrics", name)
}

func TestMetricsDropAction(t *testing.T) {
	content := `
processor:
   - name: "metrics_filter/drop"
     config:
       drop:
         metrics:
           - "my_metrics"
`
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*metricsFilter)
	assert.NoError(t, err)

	g := makeMetricsGenerator(1)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	_, err = factory.Process(&record)
	assert.NoError(t, err)

	metrics := record.Data.(pmetric.Metrics).ResourceMetrics()
	assert.Equal(t, 0, metrics.Len())
}

func TestMetricsReplaceAction(t *testing.T) {
	content := `
processor:
   - name: "metrics_filter/replace"
     config:
       replace:
         - source: my_metrics
           destination: my_metrics_replace
`
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*metricsFilter)
	assert.NoError(t, err)

	g := makeMetricsGenerator(1)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	_, err = factory.Process(&record)
	assert.NoError(t, err)

	metrics := record.Data.(pmetric.Metrics)
	assert.Equal(t, 1, metrics.ResourceMetrics().Len())

	name := testkits.FirstMetric(metrics).Name()
	assert.Equal(t, "my_metrics_replace", name)
}
