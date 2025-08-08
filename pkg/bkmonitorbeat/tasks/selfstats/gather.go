// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package selfstats

import (
	"context"
	"math"
	"strconv"

	"github.com/elastic/beats/libbeat/common"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"golang.org/x/exp/maps"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
)

type Gather struct {
	config *configs.SelfStatsConfig
	tasks.BaseTask
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.Init()

	taskConf := taskConfig.(*configs.SelfStatsConfig)
	//gather.cli = NewClient(&Option{
	//	NtpdPath:   taskConf.NtpdPath,
	//	ChronyAddr: taskConf.ChronyAddress,
	//	Timeout:    taskConf.QueryTimeout,
	//})

	return gather
}

func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	g.PreRun(ctx)
	defer g.PostRun(ctx)

	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {

	}

	var data []common.MapStr
	for i := 0; i < len(metrics); i++ {
		data = append(data, decodePromMetricFamily(metrics[i])...)
	}

	e <- &Event{
		BizID:  0,
		DataID: 0,
		Data:   data,
	}
}

func decodePromMetricFamily(mf *dto.MetricFamily) []common.MapStr {
	name := *mf.Name
	var ms []common.MapStr

	for _, metric := range mf.GetMetric() {
		lbs := map[string]string{}
		if len(metric.Label) != 0 {
			for _, label := range metric.Label {
				if label.GetName() != "" && label.GetValue() != "" {
					lbs[label.GetName()] = label.GetValue()
				}
			}
		}

		// 处理 Counter 类型数据
		counter := metric.GetCounter()
		if counter != nil && isValidFloat64(counter.GetValue()) {
			m := Metric{
				Metrics:   map[string]float64{name: counter.GetValue()},
				Timestamp: *metric.TimestampMs,
				Dimension: maps.Clone(lbs),
			}
			ms = append(ms, m.AsMapStr())
		}

		// 处理 Gauge 类型数据
		gauge := metric.GetGauge()
		if gauge != nil && isValidFloat64(gauge.GetValue()) {
			m := Metric{
				Metrics:   map[string]float64{name: gauge.GetValue()},
				Timestamp: *metric.TimestampMs,
				Dimension: maps.Clone(lbs),
			}
			ms = append(ms, m.AsMapStr())
		}

		// 处理 Summary 类型数据
		summary := metric.GetSummary()
		if summary != nil && isValidFloat64(summary.GetSampleSum()) {
			m := Metric{
				Metrics: map[string]float64{
					name + "_sum":   summary.GetSampleSum(),
					name + "_count": float64(summary.GetSampleCount()),
				},
				Timestamp: *metric.TimestampMs,
				Dimension: maps.Clone(lbs),
			}
			ms = append(ms, m.AsMapStr())

			for _, quantile := range summary.GetQuantile() {
				if !isValidFloat64(quantile.GetValue()) {
					continue
				}

				quantileLabels := maps.Clone(lbs)
				quantileLabels["quantile"] = strconv.FormatFloat(quantile.GetQuantile(), 'f', -1, 64)
				m := Metric{
					Metrics: map[string]float64{
						name: quantile.GetValue(),
					},
					Timestamp: *metric.TimestampMs,
					Dimension: quantileLabels,
				}
				ms = append(ms, m.AsMapStr())
			}

			// 处理 Histogram 类型数据
			histogram := metric.GetHistogram()
			if histogram != nil && isValidFloat64(histogram.GetSampleSum()) {
				m := Metric{
					Metrics: map[string]float64{
						name + "_sum":   histogram.GetSampleSum(),
						name + "_count": float64(histogram.GetSampleCount()),
					},
					Timestamp: *metric.TimestampMs,
					Dimension: maps.Clone(lbs),
				}
				ms = append(ms, m.AsMapStr())

				infSeen := false
				for _, bucket := range histogram.GetBucket() {
					if math.IsInf(bucket.GetUpperBound(), +1) {
						infSeen = true
					}

					bucketLabels := maps.Clone(lbs)
					bucketLabels["le"] = strconv.FormatFloat(bucket.GetUpperBound(), 'f', -1, 64)
					m := Metric{
						Metrics: map[string]float64{
							name + "_sum":   histogram.GetSampleSum(),
							name + "_count": float64(histogram.GetSampleCount()),
						},
						Timestamp: *metric.TimestampMs,
						Dimension: lbs,
					}
					ms = append(ms, m.AsMapStr())
				}

				// 仅 expfmt.FmtText 格式支持 inf
				// 其他格式需要自行检查
				if !infSeen {
					bucketLabels := maps.Clone(lbs)
					bucketLabels["le"] = strconv.FormatFloat(math.Inf(+1), 'f', -1, 64)
					m := Metric{
						Metrics: map[string]float64{
							name + "_sum":   histogram.GetSampleSum(),
							name + "_count": float64(histogram.GetSampleCount()),
						},
						Timestamp: *metric.TimestampMs,
						Dimension: bucketLabels,
					}
					ms = append(ms, m.AsMapStr())
				}
			}

			// 处理未知类型数据
			untyped := metric.GetUntyped()
			if untyped != nil && isValidFloat64(untyped.GetValue()) {
				m := Metric{
					Metrics:   map[string]float64{name: untyped.GetValue()},
					Timestamp: *metric.TimestampMs,
					Dimension: maps.Clone(lbs),
				}
				ms = append(ms, m.AsMapStr())
			}
		}
	}

	return ms
}

func isValidFloat64(f float64) bool {
	return !(math.IsNaN(f) || math.IsInf(f, 0))
}
