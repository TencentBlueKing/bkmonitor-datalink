// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pre_calculate

import (
	"bytes"
	"io"
	"net/http"
	"time"

	"github.com/grafana/pyroscope-go"
	jsoniter "github.com/json-iterator/go"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/window"
)

type metricConfig struct {
	name        string
	dataId      int
	accessToken string
}

type metric struct {
	m           []metricConfig
	collectFunc func(*RunInstance, metric, func(metric, int, map[string]string, int))
}

type MetricCollector struct {
	MetricOptions
}

type MetricOption func(options *MetricOptions)

type MetricOptions struct {
	enabled bool

	host        string
	validMetric []*metric

	enabledProfile bool
	interval       time.Duration
	profileAddress string
}

func ReportHost(h string) MetricOption {
	return func(options *MetricOptions) {
		options.host = h
	}
}

func EnabledMetric(e bool) MetricOption {
	return func(options *MetricOptions) {
		options.enabled = e
	}
}

func EnabledMetricReportInterval(i int) MetricOption {
	return func(options *MetricOptions) {
		options.interval = time.Duration(i) * time.Millisecond
	}
}

func EnabledProfile(e bool) MetricOption {
	return func(options *MetricOptions) {
		options.enabledProfile = e
	}
}

func ProfileAddress(h string) MetricOption {
	return func(options *MetricOptions) {
		options.profileAddress = h
	}
}

func SaveRequestCountMetric(dataId int, at string) MetricOption {
	return func(options *MetricOptions) {
		if dataId != 0 && at != "" {
			options.validMetric = append(
				options.validMetric,
				&metric{
					m:           []metricConfig{{name: "SaveRequestCountMetric", dataId: dataId, accessToken: at}},
					collectFunc: saveRequestCountReporter},
			)
		}
	}
}

func MessageChanCountMetric(dataId int, at string) MetricOption {
	return func(options *MetricOptions) {
		if dataId != 0 && at != "" {
			options.validMetric = append(
				options.validMetric,
				&metric{
					m:           []metricConfig{{name: "messageChanCountMetric", dataId: dataId, accessToken: at}},
					collectFunc: messageCountReporter},
			)
		}
	}
}

func WindowTraceAndSpanCountMetric(spanCountDataId int, spanCountAt string, traceCountDataId int, traceCountAt string) MetricOption {
	return func(options *MetricOptions) {

		if spanCountDataId != 0 && spanCountAt != "" && traceCountDataId != 0 && traceCountAt != "" {
			options.validMetric = append(
				options.validMetric,
				&metric{
					m: []metricConfig{
						{name: "windowSpanCountMetric", dataId: spanCountDataId, accessToken: spanCountAt},
						{name: "windowTraceCountMetric", dataId: traceCountDataId, accessToken: traceCountAt},
					},
					collectFunc: windowTraceAndSpanCountReporter,
				},
			)
		}
	}
}

func EsTraceCountMetric(originCountDataId int, originCountAt string, preCalDataId int, preCalAt string) MetricOption {
	return func(options *MetricOptions) {

		if originCountDataId != 0 && originCountAt != "" && preCalDataId != 0 && preCalAt != "" {
			options.validMetric = append(
				options.validMetric,
				&metric{
					m: []metricConfig{
						{name: "originEsTraceCount", dataId: originCountDataId, accessToken: originCountAt},
						{name: "preCalEsTraceCount", dataId: preCalDataId, accessToken: preCalAt},
					},
					collectFunc: esTraceCountReporter,
				},
			)
		}

	}
}

func NewMetricCollector(o MetricOptions) MetricCollector {
	return MetricCollector{MetricOptions: o}
}

func (r *MetricCollector) StartReport(runIns *RunInstance) {
	if r.enabled {
		for _, f := range r.validMetric {
			go f.collectFunc(runIns, *f, func(metric metric, v int, dimension map[string]string, mIndex int) {
				r.ReportToServer(metric, v, dimension, mIndex)
			})
		}

		if r.enabledProfile {
			r.startProfiling(runIns.dataId)
		} else {
			apmLogger.Infof("Profiling disabled.")
		}
	}
}

func saveRequestCountReporter(r *RunInstance, m metric, postHandle func(metric, int, map[string]string, int)) {
	dimension := map[string]string{
		"dataId": r.dataId,
	}
loop:
	for {
		select {
		case <-r.ctx.Done():
			apmLogger.Info("Metric: saveRequestCount report stopped.")
			break loop
		default:
			v := len(r.proxy.SaveRequest())
			postHandle(m, v, dimension, 0)
			time.Sleep(r.metricCollector.interval)
		}
	}
}

func messageCountReporter(r *RunInstance, f metric, postHandle func(metric, int, map[string]string, int)) {
	dimension := map[string]string{
		"dataId": r.dataId,
	}

loop:
	for {
		select {
		case <-r.ctx.Done():
			apmLogger.Info("Metric: messageCount report stopped.")
			break loop
		default:
			v := len(r.notifier.Spans())
			postHandle(f, v, dimension, 0)
			time.Sleep(r.metricCollector.interval)
		}
	}
}

func windowTraceAndSpanCountReporter(r *RunInstance, metric metric, postHandle func(metric, int, map[string]string, int)) {

loop:
	for {
		select {
		case <-r.ctx.Done():
			apmLogger.Info("Metric: TraceAndSpanCount report stopped.")
			break loop
		default:
			data := r.windowHandler.ReportMetric()
			for k, items := range data {
				var mIndex int
				if k == window.TraceCount {
					mIndex = 1
				} else {
					mIndex = 0
				}
				for _, m := range items {
					postHandle(metric, m.Value, m.Dimension, mIndex)
				}
			}
			time.Sleep(r.metricCollector.interval)
		}
	}
}

func esTraceCountReporter(r *RunInstance, metric metric, postHandle func(metric, int, map[string]string, int)) {

	now := time.Now()
	next := now.Add(time.Minute)
	next = time.Date(next.Year(), next.Month(), next.Day(), next.Hour(), next.Minute(), 0, 0, next.Location())
	duration := next.Sub(now)
	time.Sleep(duration)
	ticker := time.NewTicker(1 * time.Minute)
	apmLogger.Infof("Metric: OriginTraceCount/PreCalTraceCount start at %s", time.Now().Format(time.Kitchen))

	dimension := map[string]string{
		"dataId": r.dataId,
	}

	baseInfo := core.GetMetadataCenter().GetBaseInfo(r.dataId)

	aggsName := "unique_trace_ids_count"
loop:
	for {
		select {
		case <-r.ctx.Done():
			apmLogger.Info("Metric: OriginTraceCount/PreCalTraceCount report stopped.")
			break loop
		case <-ticker.C:
			n := time.Now()

			startTime := time.Date(n.Year(), n.Month(), n.Day(), n.Hour(), n.Minute()-1, 0, 0, n.Location())
			endTime := startTime.Add(time.Minute)

			traceIdAggsQuery := func(isNano bool, filter []map[string]any) storage.EsQueryData {
				var s, e int64
				if isNano {
					s = startTime.UnixNano()
					e = endTime.UnixNano()
				} else {
					s = startTime.UnixMilli()
					e = endTime.UnixMilli()
				}

				f := append([]map[string]any{{"range": map[string]any{"time": map[string]int64{"gte": s, "lte": e}}}}, filter...)
				return storage.EsQueryData{
					Converter: storage.AggsCountConvert,
					Body: map[string]any{
						"size": 0,
						"query": map[string]any{
							"bool": map[string]any{
								"filter": f,
							},
						},
						"aggs": map[string]any{
							aggsName: map[string]any{
								"cardinality": map[string]string{
									"field": "trace_id",
								},
							},
						},
					},
				}
			}

			traceEsCount, traceErr := r.proxy.Query(storage.QueryRequest{
				Target: storage.TraceEs,
				Data:   traceIdAggsQuery(false, []map[string]any{}),
			},
			)
			if traceErr != nil {
				apmLogger.Errorf("Query OriginTraceES count failed, error: %s", traceErr)
			} else {
				traceEsCountM := traceEsCount.(map[string]int)
				if len(traceEsCountM) != 0 {
					postHandle(metric, traceEsCountM[aggsName], dimension, 0)
				}
			}

			saveEsCount, saveErr := r.proxy.Query(storage.QueryRequest{
				Target: storage.SaveEs,
				Data: traceIdAggsQuery(true,
					[]map[string]any{
						{
							"term": map[string]string{
								"biz_id": baseInfo.BkBizId,
							},
						},
						{
							"term": map[string]string{
								"app_name": baseInfo.AppName,
							},
						},
					}),
			},
			)
			if saveErr != nil {
				apmLogger.Errorf("Query PreCalTraceES count failed, error: %s", saveErr)
			} else {
				saveEsCountM := saveEsCount.(map[string]int)
				if len(saveEsCountM) != 0 {
					postHandle(metric, saveEsCountM[aggsName], dimension, 1)
				}
			}

			time.Sleep(r.metricCollector.interval)
		}
	}
}

func (r *MetricCollector) ReportToServer(m metric, v int, dimension map[string]string, mIndex int) {
	data := map[string]any{
		"data_id":      m.m[mIndex].dataId,
		"access_token": m.m[mIndex].accessToken,
		"data": []map[string]any{
			{
				"metrics": map[string]any{
					"value": v,
				},
				"target":    "metric-collector",
				"dimension": dimension,
			},
		},
	}
	jsonData, err := jsoniter.Marshal(data)
	if err != nil {
		apmLogger.Errorf("Parsing json data failed. This metric: %s will not be reported. error: %s", m.m[mIndex].name, err)
		return
	}

	req, err := http.NewRequest("POST", r.host, bytes.NewBuffer(jsonData))
	if err != nil {
		apmLogger.Errorf("Create request failed. This metric: %s will not be reported. error: %s", m.m[mIndex].name, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	// 发送请求并获取响应
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		apmLogger.Errorf("Post request failed. This metric: %s will not be reported. error: %s", m.m[mIndex].name, err)
		return
	}
	defer func(Body io.ReadCloser) {
		err = Body.Close()
		if err != nil {
			apmLogger.Errorf("Close response body failed. error: %s", err)
		}
	}(resp.Body)
}

func (r *MetricCollector) startProfiling(dataId string) {

	n := "apm_precalculate_application"
	_, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: n,
		ServerAddress:   r.profileAddress,
		Logger:          apmLogger,
		Tags:            map[string]string{"dataId": dataId},
		ProfileTypes: []pyroscope.ProfileType{
			// these profile types are enabled by default:
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,

			// these profile types are optional:
			pyroscope.ProfileGoroutines,
			pyroscope.ProfileMutexCount,
			pyroscope.ProfileMutexDuration,
			pyroscope.ProfileBlockCount,
			pyroscope.ProfileBlockDuration,
		},
	})

	if err != nil {
		apmLogger.Errorf("Start pyroscope failed, err: %s", err)
		return
	}
	apmLogger.Infof("Start profiling at %s(name: %s)", r.profileAddress, n)
}
