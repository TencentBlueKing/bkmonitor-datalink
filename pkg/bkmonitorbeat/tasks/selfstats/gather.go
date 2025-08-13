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
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"golang.org/x/exp/maps"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var startTime = time.Now()

type Gather struct {
	tasks.BaseTask
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.Init()
	return gather
}

func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	g.PreRun(ctx)
	defer g.PostRun(ctx)

	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		logger.Errorf("failed to load prometheus gather: %v", err)
		return
	}

	info, _ := gse.GetAgentInfo()
	extLabels := map[string]string{
		"bk_cloud_id":  strconv.Itoa(int(info.Cloudid)),
		"bk_target_ip": info.IP,
		"bk_agent_id":  info.BKAgentID,
		"bk_host_id":   strconv.Itoa(int(info.HostID)),
		"bk_biz_id":    strconv.Itoa(int(info.BKBizID)),
		"node_id":      fmt.Sprintf("%d:%s", info.Cloudid, info.IP),
		"hostname":     info.Hostname,
	}

	var data []common.MapStr
	for i := 0; i < len(metrics); i++ {
		data = append(data, decodePromMetricFamily(metrics[i], extLabels)...)
	}

	lbs := make(map[string]string)
	for k, v := range extLabels {
		lbs[k] = v
	}
	rs := define.GetRuntimeStats()
	lbs["version"] = rs.Version
	data = append(data, buildMetrics("version", 1, lbs))

	data = append(data, buildMetrics("uptime", time.Since(startTime).Seconds(), extLabels))
	data = append(data, buildMetrics("reload_total", float64(rs.Reload), extLabels))

	e <- &Event{
		BizID:  g.TaskConfig.GetBizID(),
		DataID: g.TaskConfig.GetDataID(),
		Data:   data,
	}
}

func getTimestampMs(now int64, t *int64) int64 {
	if t != nil {
		return *t
	}
	return now
}

func isValidFloat64(f float64) bool {
	return !(math.IsNaN(f) || math.IsInf(f, 0))
}

func buildMetrics(name string, value float64, labels map[string]string) common.MapStr {
	lbs := make(map[string]string)
	for k, v := range labels {
		lbs[k] = v
	}

	m := Metric{
		Metrics:   map[string]float64{"bkmonitorbeat_" + name: value},
		Timestamp: time.Now().UnixMilli(),
		Dimension: labels,
	}
	return m.AsMapStr()
}

func decodePromMetricFamily(mf *dto.MetricFamily, extLabels map[string]string) []common.MapStr {
	var ms []Metric
	name := *mf.Name
	now := time.Now().UnixMilli()

	for _, metric := range mf.GetMetric() {
		lbs := map[string]string{}
		if len(metric.Label) != 0 {
			for _, label := range metric.Label {
				if label.GetName() != "" && label.GetValue() != "" {
					lbs[label.GetName()] = label.GetValue()
				}
			}
		}
		for k, v := range extLabels {
			lbs[k] = v
		}

		ts := getTimestampMs(now, metric.TimestampMs)

		// 处理 Counter 类型数据
		counter := metric.GetCounter()
		if counter != nil && isValidFloat64(counter.GetValue()) {
			ms = append(ms, Metric{
				Metrics:   map[string]float64{name: counter.GetValue()},
				Timestamp: ts,
				Dimension: maps.Clone(lbs),
			})
		}

		// 处理 Gauge 类型数据
		gauge := metric.GetGauge()
		if gauge != nil && isValidFloat64(gauge.GetValue()) {
			ms = append(ms, Metric{
				Metrics:   map[string]float64{name: gauge.GetValue()},
				Timestamp: ts,
				Dimension: maps.Clone(lbs),
			})
		}

		// 处理 Summary 类型数据
		summary := metric.GetSummary()
		if summary != nil && isValidFloat64(summary.GetSampleSum()) {
			ms = append(ms, Metric{
				Metrics: map[string]float64{
					name + "_sum":   summary.GetSampleSum(),
					name + "_count": float64(summary.GetSampleCount()),
				},
				Timestamp: ts,
				Dimension: maps.Clone(lbs),
			})

			for _, quantile := range summary.GetQuantile() {
				if !isValidFloat64(quantile.GetValue()) {
					continue
				}

				quantileLabels := maps.Clone(lbs)
				quantileLabels["quantile"] = strconv.FormatFloat(quantile.GetQuantile(), 'f', -1, 64)
				ms = append(ms, Metric{
					Metrics: map[string]float64{
						name: quantile.GetValue(),
					},
					Timestamp: ts,
					Dimension: quantileLabels,
				})
			}

			// 处理 Histogram 类型数据
			histogram := metric.GetHistogram()
			if histogram != nil && isValidFloat64(histogram.GetSampleSum()) {
				ms = append(ms, Metric{
					Metrics: map[string]float64{
						name + "_sum":   histogram.GetSampleSum(),
						name + "_count": float64(histogram.GetSampleCount()),
					},
					Timestamp: ts,
					Dimension: maps.Clone(lbs),
				})

				infSeen := false
				for _, bucket := range histogram.GetBucket() {
					if math.IsInf(bucket.GetUpperBound(), +1) {
						infSeen = true
					}

					bucketLabels := maps.Clone(lbs)
					bucketLabels["le"] = strconv.FormatFloat(bucket.GetUpperBound(), 'f', -1, 64)
					ms = append(ms, Metric{
						Metrics: map[string]float64{
							name + "_sum":   histogram.GetSampleSum(),
							name + "_count": float64(histogram.GetSampleCount()),
						},
						Timestamp: ts,
						Dimension: bucketLabels,
					})
				}

				// 仅 expfmt.FmtText 格式支持 inf
				// 其他格式需要自行检查
				if !infSeen {
					bucketLabels := maps.Clone(lbs)
					bucketLabels["le"] = strconv.FormatFloat(math.Inf(+1), 'f', -1, 64)
					ms = append(ms, Metric{
						Metrics: map[string]float64{
							name + "_sum":   histogram.GetSampleSum(),
							name + "_count": float64(histogram.GetSampleCount()),
						},
						Timestamp: ts,
						Dimension: bucketLabels,
					})
				}
			}

			// 处理未知类型数据
			untyped := metric.GetUntyped()
			if untyped != nil && isValidFloat64(untyped.GetValue()) {
				ms = append(ms, Metric{
					Metrics:   map[string]float64{name: untyped.GetValue()},
					Timestamp: ts,
					Dimension: maps.Clone(lbs),
				})
			}
		}
	}

	ret := make([]common.MapStr, 0, len(ms))
	for i := 0; i < len(ms); i++ {
		ret = append(ret, ms[i].AsMapStr())
	}
	return ret
}
