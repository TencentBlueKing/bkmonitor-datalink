// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/metricbeat/mb"
	"github.com/elastic/beats/metricbeat/mb/parse"
	"github.com/prometheus/prometheus/model/relabel"
	"gopkg.in/yaml.v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	defaultScheme = "http"
	defaultPath   = "/metrics"
	metricName    = "__name__"
)

const (
	upMetric = "bkm_metricbeat_endpoint_up"
)

func newMetricUp(code int) string {
	return fmt.Sprintf(`%s{code="%d"} 1`, upMetric, code)
}

var (
	hostParser = parse.URLHostParserBuilder{
		DefaultScheme: defaultScheme,
		DefaultPath:   defaultPath,
		PathConfigKey: "metrics_path",
	}.Build()
)

func init() {
	mb.Registry.MustAddMetricSet("prometheus", "collector", New, mb.WithHostParser(hostParser))
}

// MetricSet :
type MetricSet struct {
	mb.BaseMetricSet
	httpClient *HTTPClient
	namespace  string

	deltaKeys        map[string]struct{}
	lastDeltaMetrics map[string]map[string]float64 // map[metricName]map[hash]value

	useTempFile     bool
	tempFilePattern string

	MetricReplace    map[string]string
	DimensionReplace map[string]string

	remoteClient           *http.Client
	workers                int
	disableCustomTimestamp bool
	remoteRelabelCache     []*relabel.Config
	MetricRelabelRemote    string
	MetricRelabelConfigs   []*relabel.Config
}

// New :
func New(base mb.BaseMetricSet) (mb.MetricSet, error) {
	config := struct {
		Namespace                  string            `config:"namespace" validate:"required"`
		DiffMetrics                []string          `config:"diff_metrics"`
		MetricReplace              map[string]string `config:"metric_replace"`
		DimensionReplace           map[string]string `config:"dimension_replace"`
		MetricRelabelConfigs       interface{}       `config:"metric_relabel_configs"`
		MetricRelabelRemote        string            `config:"metric_relabel_remote"`
		MetricRelabelRemoteTimeout string            `config:"metric_relabel_remote_timeout"`
		TempFilePattern            string            `config:"temp_file_pattern"`
		Workers                    int               `config:"workers"`
		DisableCustomTimestamp     bool              `config:"disable_custom_timestamp"`
	}{}

	if err := base.Module().UnpackConfig(&config); err != nil {
		logger.Errorf("unpack failed, error: %s", err)
		return nil, err
	}
	logger.Debugf("base.metric.set config: %+v", config)

	deltaKeys := map[string]struct{}{}
	lastDeltaMetrics := make(map[string]map[string]float64)
	for _, key := range config.DiffMetrics {
		deltaKeys[key] = struct{}{}
		lastDeltaMetrics[key] = make(map[string]float64)
	}

	var relabels []*relabel.Config
	data, err := yaml.Marshal(config.MetricRelabelConfigs)
	if err != nil {
		logger.Errorf("marshal metric relabel config failed, error: %s", err)
		return nil, err
	}

	logger.Debugf("get metric relabel config: %s", data)
	if err = yaml.Unmarshal(data, &relabels); err != nil {
		logger.Errorf("unmarshal metric relabel config failed, error: %s", err)
		return nil, err
	}
	logger.Debugf("get metric relabel struct: %v", relabels)

	duration := time.Second * 3
	if config.MetricRelabelRemoteTimeout != "" {
		d, err := time.ParseDuration(config.MetricRelabelRemoteTimeout)
		if err != nil {
			logger.Errorf("failed to parse remote timeout config: %v", err)
		} else {
			duration = d
		}
	}

	httpClient, err := NewHTTPClient(base)
	if err != nil {
		logger.Errorf("failed to create HTTP client: %v", err)
		return nil, err
	}

	return &MetricSet{
		BaseMetricSet:          base,
		httpClient:             httpClient,
		namespace:              config.Namespace,
		deltaKeys:              deltaKeys,
		lastDeltaMetrics:       lastDeltaMetrics,
		useTempFile:            utils.HasTempDir(),
		tempFilePattern:        config.TempFilePattern,
		remoteClient:           &http.Client{Timeout: duration},
		MetricRelabelRemote:    config.MetricRelabelRemote,
		MetricReplace:          config.MetricReplace,
		DimensionReplace:       config.DimensionReplace,
		MetricRelabelConfigs:   relabels,
		disableCustomTimestamp: config.DisableCustomTimestamp,
		workers:                config.Workers,
	}, nil
}

func (m *MetricSet) getEventFromPromEvent(promEvent *tasks.PromEvent) (common.MapStr, *diffKey) {
	// 执行 relabels 规则
	if len(m.MetricRelabelConfigs) != 0 {
		if !m.metricRelabel(promEvent) {
			return nil, nil
		}
	}

	// 基于配置进行维度复制
	if m.DimensionReplace != nil {
		m.replaceDimensions(promEvent)
	}

	// labels 处理
	event := common.MapStr{}
	event["key"] = promEvent.Key
	event["labels"] = common.MapStr{}
	if len(promEvent.Labels) > 0 {
		event["labels"] = promEvent.Labels
	}

	// 如果不禁用 custom timestamp 则需要把时间戳补上
	if !m.disableCustomTimestamp {
		event["timestamp"] = promEvent.TS
	}

	// exemplar 处理
	if promEvent.Exemplar != nil && promEvent.Exemplar.Ts > 0 {
		exemplarLbs := make(map[string]string)
		for _, pair := range promEvent.Exemplar.Labels {
			exemplarLbs[pair.Name] = pair.Value
		}

		// 允许只提供 traceID 或者只提供 spanID
		tmp := common.MapStr{}
		traceID, spanID := tasks.MatchTraces(exemplarLbs)
		if traceID != "" {
			tmp["bk_trace_id"] = traceID
		}
		if spanID != "" {
			tmp["bk_span_id"] = spanID
		}
		if len(tmp) > 0 {
			tmp["bk_trace_timestamp"] = promEvent.Exemplar.Ts
			tmp["bk_trace_value"] = promEvent.Exemplar.Value
			event["exemplar"] = tmp
		}
	}

	// 差值计算
	var dk *diffKey
	lines, ok := m.lastDeltaMetrics[promEvent.Key]
	if ok {
		currValue := promEvent.Value
		lastValue, ok := lines[promEvent.HashKey]
		if ok {
			event["value"] = currValue - lastValue
		}
		dk = &diffKey{
			key:   promEvent.Key,
			hash:  promEvent.HashKey,
			value: currValue,
		}
	} else {
		event["value"] = promEvent.Value
	}
	return event, dk
}

// getEventsFromFile 从文件获取指标
func (m *MetricSet) getEventsFromFile(fileName string) (<-chan common.MapStr, error) {
	f, err := os.Open(fileName)
	if err != nil {
		logger.Errorf("open metricsFile failed, err: %v", err)
		return nil, err
	}

	cleanup := func() {
		if err := f.Close(); err != nil {
			logger.Errorf("close metricsFile: %s failed, err: %v", fileName, err)
		}
		if err := os.Remove(fileName); err != nil {
			logger.Errorf("remove metricsFile: %s failed, err: %v", fileName, err)
		}
	}
	// 如果已经是从文件读取 表示拉取成功
	return m.getEventsFromReader(f, cleanup, true), nil
}

type diffKey struct {
	key   string
	hash  string
	value float64
}

// getEventsFromReader 从 reader 获取指标
func (m *MetricSet) getEventsFromReader(metricsReader io.ReadCloser, cleanup func(), up bool) <-chan common.MapStr {
	if m.MetricRelabelRemote != "" {
		remoteRelabelConfigs, err := m.getRemoteRelabelConfigs()
		if err != nil {
			logger.Errorf("failed to get remote relabel configs: %v", err)
		} else {
			m.remoteRelabelCache = remoteRelabelConfigs
		}
	}

	scanner := bufio.NewScanner(metricsReader)
	linesCh := make(chan string, 1)
	go func() {
		for scanner.Scan() {
			linesCh <- scanner.Text()
		}
		close(linesCh)
	}()

	worker := m.workers
	if worker <= 0 {
		worker = 1
	}
	milliTs := time.Now().UnixMilli()
	eventChan := make(chan common.MapStr)

	go func() {
		defer close(eventChan)
		defer cleanup()

		var lastDiffMetricMut sync.Mutex
		lastDiffMetrics := make(map[string]map[string]float64)
		for key := range m.deltaKeys {
			lastDiffMetrics[key] = make(map[string]float64)
		}

		wg := sync.WaitGroup{}
		for i := 0; i < worker; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				var upErr *define.BeaterUpMetricErr
				for line := range linesCh {
					events, dk, err := m.produceEvents(line, milliTs)
					if err != nil {
						errors.As(err, &upErr)
					}
					if dk != nil {
						lastDiffMetricMut.Lock()
						lastDiffMetrics[dk.key][dk.hash] = dk.value
						lastDiffMetricMut.Unlock()
					}
					for j := 0; j < len(events); j++ {
						eventChan <- events[j]
					}
				}
				// 遍历解析 Prom 语句存在错误时则传入异常状态码，无错误则传入OK状态码
				if up {
					var events []common.MapStr
					if upErr == nil {
						events, _, _ = m.produceEvents(newMetricUp(define.BeatErrCodeOK), milliTs)
					} else {
						events, _, _ = m.produceEvents(newMetricUp(upErr.Code), milliTs)
					}
					if len(events) > 0 {
						eventChan <- events[0]
					}
				}
			}()
		}
		wg.Wait()
		m.lastDeltaMetrics = lastDiffMetrics
	}()
	return eventChan
}

func (m *MetricSet) produceEvents(line string, timestamp int64) ([]common.MapStr, *diffKey, error) {
	if len(line) <= 0 || line[0] == '#' {
		return nil, nil, nil
	}

	timeOffset := 24 * time.Hour * 365 * 2 // 默认可容忍偏移时间为两年
	tsHandler, _ := tasks.GetTimestampHandler("s")
	promEvent, err := tasks.NewPromEvent(line, timestamp, timeOffset, tsHandler)
	if err != nil {
		errMsg := fmt.Sprintf("parse line=>(%s) failed, err: %s", line, err)
		upErr := &define.BeaterUpMetricErr{Code: define.BeatMetricBeatPromFormatOuterError, Message: errMsg}
		logger.Warnf(upErr.Error())
		return nil, nil, upErr
	}

	// 生成事件
	var events []common.MapStr
	event, dk := m.getEventFromPromEvent(&promEvent)
	if event == nil {
		return nil, nil, nil
	}
	events = append(events, event)

	// 基于配置进行指标复制
	targetMetricKey := m.getTargetMetricKey(&promEvent)
	if targetMetricKey != "" {
		targetEvent := event.Clone()
		targetEvent["key"] = targetMetricKey
		events = append(events, targetEvent)
	}
	return events, dk, nil
}

func newFailReader(code int) io.ReadCloser {
	r := bytes.NewReader([]byte(newMetricUp(code)))
	return io.NopCloser(r)
}

// Fetch 采集逻辑入口
func (m *MetricSet) Fetch() (common.MapStr, error) {
	var err error
	summary := common.MapStr{}

	startTime := time.Now()
	rsp, err := m.httpClient.FetchResponse()
	logger.Debugf("httpClient response %s, take: %v", m.Host(), time.Since(startTime))
	if err != nil {
		logger.Errorf("failed to get data, err: %v", err)
		m.fillMetrics(summary, newFailReader(define.BeatMetricBeatConnOuterError), false)
		return summary, err
	}
	defer rsp.Body.Close()

	var metricsFile *os.File
	if m.useTempFile {
		metricsFile, err = utils.CreateTempFile(m.tempFilePattern)
		if err != nil {
			logger.Errorf("create metricsFile failed, err: %v", err)
			m.fillMetrics(summary, newFailReader(define.BeaterMetricBeatWriteTmpFileError), false)
			return summary, err
		}

		if _, err = io.Copy(metricsFile, rsp.Body); err != nil {
			logger.Errorf("write metricsFile failed, err: %v", err)
			m.fillMetrics(summary, newFailReader(define.BeaterMetricBeatWriteTmpFileError), false)
			_ = metricsFile.Close()
			_ = os.Remove(metricsFile.Name())
			return summary, err
		}
		if err = metricsFile.Close(); err != nil {
			logger.Errorf("close metricsFile failed, err: %v", err)
			m.fillMetrics(summary, newFailReader(define.BeaterMetricBeatWriteTmpFileError), false)
			_ = os.Remove(metricsFile.Name())
			return summary, err
		}
	}

	// 解析prometheus数据
	if m.useTempFile {
		summary["metrics_reader"] = define.MetricsReaderFunc(func() (<-chan common.MapStr, error) {
			return m.getEventsFromFile(metricsFile.Name())
		})
	} else {
		m.fillMetrics(summary, rsp.Body, true)
	}
	summary["namespace"] = m.namespace
	logger.Debugf("end fetch data from: %s, get summary data: %s", m.Host(), summary["namespace"])
	return summary, err
}

func (m *MetricSet) fillMetrics(summary common.MapStr, rc io.ReadCloser, up bool) {
	events := make([]common.MapStr, 0)
	for event := range m.getEventsFromReader(rc, func() {}, up) {
		events = append(events, event)
	}
	summary["metrics"] = events
	logger.Debugf("got metrics count: %d", len(events))
}
