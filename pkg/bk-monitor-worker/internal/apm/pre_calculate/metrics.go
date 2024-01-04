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
	"fmt"
	"net/http"
	"time"

	"github.com/grafana/pyroscope-go"
	jsoniter "github.com/json-iterator/go"
	"golang.org/x/exp/maps"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type metric string

var (
	SaveRequestChanCount    metric = "SaveRequestCountMetric"
	MessageReceiveChanCount metric = "MessageChanCountMetric"
	WindowMetric            metric = "WindowMetric"
	EsTraceMetric           metric = "EsTraceMetric"
)

var metricHandlerMapping = map[metric]func(*RunInstance, MetricOptions) (map[string]float64, error){
	SaveRequestChanCount:    saveRequestCountReporter,
	MessageReceiveChanCount: messageCountReporter,
	WindowMetric:            windowTraceAndSpanCountReporter,
	EsTraceMetric:           esTraceCountReporter,
}

type MetricCollector struct {
	config        MetricOptions
	httpTransport *http.Transport
	trigger       func() error
	runInstance   *RunInstance
}

type MetricOption func(options *MetricOptions)

type MetricOptions struct {
	enabledMetric        bool
	metricReportHost     string
	metrics              []metric
	metricReportInterval time.Duration
	metricsDataId        int
	metricsAccessToken   string

	enabledProfile bool
	profileAddress string
	profileAppIdx  string
}

// EnabledMetricReport Whether to enable indicator reporting.
func EnabledMetricReport(e bool) MetricOption {
	return func(options *MetricOptions) {
		if !e {
			logger.Infof("metric report is disabled.")
		}
		options.enabledMetric = e
	}
}

// MetricReportHost indicator report host
func MetricReportHost(h string) MetricOption {
	return func(options *MetricOptions) {
		options.metricReportHost = h
	}
}

func ReportMetrics(m ...metric) MetricOption {
	return func(options *MetricOptions) {
		options.metrics = m
	}
}

// EnabledMetricReportInterval Indicator reporting interval.
func EnabledMetricReportInterval(i time.Duration) MetricOption {
	return func(options *MetricOptions) {
		options.metricReportInterval = i
	}
}

// MetricReportDataId indicator report data id
func MetricReportDataId(d int) MetricOption {
	return func(options *MetricOptions) {
		options.metricsDataId = d
	}
}

// MetricReportAccessToken indicator report access token
func MetricReportAccessToken(d string) MetricOption {
	return func(options *MetricOptions) {
		options.metricsAccessToken = d
	}
}

// EnabledProfileReport Whether to enable indicator reporting.
func EnabledProfileReport(e bool) MetricOption {
	return func(options *MetricOptions) {
		if !e {
			logger.Infof("profile report is disabled.")
		}
		options.enabledProfile = e
	}
}

// ProfileAddress profile report host
func ProfileAddress(h string) MetricOption {
	return func(options *MetricOptions) {
		options.profileAddress = h
	}
}

// ProfileAppIdx app name of profile
func ProfileAppIdx(h string) MetricOption {
	return func(options *MetricOptions) {
		if h != "" {
			options.profileAppIdx = h
			return
		}
		defaultV := "apm_precalculate"
		logger.Infof("profile appIdx is not specified, %s is used as the default", defaultV)
		options.profileAppIdx = defaultV
	}
}

func NewMetricCollector(o MetricOptions, transport *http.Transport, instance *RunInstance) MetricCollector {

	trigger := func() error {
		reportValues := make(map[string]float64)
		for _, m := range o.metrics {
			values, err := metricHandlerMapping[m](instance, o)
			if err != nil {
				logger.Errorf("failed to get value of metric: %s, error: %s", m, err)
				continue
			}
			maps.Copy(reportValues, values)
		}
		return ReportToServer(
			transport,
			o.metricReportHost,
			o.metricsDataId,
			o.metricsAccessToken,
			instance.dataId,
			reportValues,
		)
	}

	return MetricCollector{config: o, httpTransport: transport, trigger: trigger, runInstance: instance}
}

func (r *MetricCollector) StartReport() {

	if r.config.enabledProfile {
		r.startProfiling(r.runInstance.dataId, r.config.profileAppIdx)
	}

	if r.config.enabledMetric && r.config.metricsDataId != 0 && r.config.metricsAccessToken != "" {

		apmLogger.Infof(
			"Start metric report in %s dataId: %s accessToken %s host: %s",
			r.config.metricReportInterval,
			r.config.metricsDataId, r.config.metricsAccessToken, r.config.metricReportHost,
		)
		go func() {
			for {
				select {
				case <-r.runInstance.ctx.Done():
					logger.Infof("metric report done")
					return
				default:
					if err := r.trigger(); err != nil {
						logger.Warnf("failed to report metric, error: %s", err)
					}
					time.Sleep(r.config.metricReportInterval)
				}
			}
		}()
	}
}

func saveRequestCountReporter(r *RunInstance, _ MetricOptions) (map[string]float64, error) {
	res := make(map[string]float64)
	res["SaveRequestCount"] = float64(len(r.proxy.SaveRequest()))
	return res, nil
}

func messageCountReporter(r *RunInstance, _ MetricOptions) (map[string]float64, error) {
	res := make(map[string]float64)
	res["ReceiveMessageCount"] = float64(len(r.notifier.Spans()))
	return res, nil
}

func windowTraceAndSpanCountReporter(r *RunInstance, _ MetricOptions) (map[string]float64, error) {

	res := make(map[string]float64)

	data := r.windowHandler.ReportMetric()

	for k, v := range data {
		res[string(k)] = float64(v)
	}
	return res, nil
}

func truncateToMinute(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, t.Location())
}

// esTraceCountReporter This is the test indicator. Do not use it in the production environment.
// In order to check whether the trace count in pre-Calculate index is consistent with that in the original table.
func esTraceCountReporter(r *RunInstance, o MetricOptions) (map[string]float64, error) {

	now := time.Now()
	// get the value within the range according to the reporting interval
	startTime := truncateToMinute(now.Add(-o.metricReportInterval))
	endTime := truncateToMinute(now)

	baseInfo := core.GetMetadataCenter().GetBaseInfo(r.dataId)
	aggsName := "unique_trace_ids_count"
	res := make(map[string]float64)
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
		return res, fmt.Errorf("query OriginTraceES count failed, error: %s", traceErr)
	}

	traceEsCountM := traceEsCount.(map[string]int)
	if len(traceEsCountM) != 0 {
		res["traceEsCount"] = float64(traceEsCountM[aggsName])
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
		return res, fmt.Errorf("query PreCalTraceES count failed, error: %s", saveErr)
	}

	saveEsCountM := saveEsCount.(map[string]int)
	if len(saveEsCountM) != 0 {
		res["saveEsCount"] = float64(saveEsCountM[aggsName])
	}

	return res, nil
}

func ReportToServer(
	httpClient *http.Transport,
	reportHost string,
	reportDataId int, reportAccessToken string, dataId string,
	values map[string]float64,
) error {

	data := map[string]any{
		"data_id":      reportDataId,
		"access_token": reportAccessToken,
		"data": []map[string]any{
			{
				"metrics": values,
				"target":  "metric-collector",
				"dimension": map[string]string{
					"dataId": dataId,
				},
			},
		},
	}
	jsonData, err := jsoniter.Marshal(data)
	if err != nil {
		return fmt.Errorf("parsing json data failed. error: %s", err)
	}

	req, err := http.NewRequest("POST", reportHost, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("create request failed, error: %s", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// 发送请求并获取响应
	client := &http.Client{Transport: httpClient}
	resp, err := client.Do(req)
	defer func() {
		if resp != nil {
			err = resp.Body.Close()
			if err != nil {
				apmLogger.Errorf("Close response body failed. error: %s", err)
			}
		}
	}()

	if err != nil {
		return fmt.Errorf("post request failed, error: %s", err)
	}

	return nil
}

func (r *MetricCollector) startProfiling(dataId, appIdx string) {

	n := fmt.Sprintf("apm_precalculate-%s", appIdx)
	_, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: n,
		ServerAddress:   r.config.profileAddress,
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
	apmLogger.Infof("Start profiling at %s(name: %s)", r.config.profileAddress, n)
}
