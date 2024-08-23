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
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/metricbeat/mb"
	"github.com/elastic/beats/metricbeat/mb/parse"
	"github.com/pkg/errors"
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

var hostParser = parse.URLHostParserBuilder{
	DefaultScheme: defaultScheme,
	DefaultPath:   defaultPath,
	PathConfigKey: "metrics_path",
}.Build()

func init() {
	mb.Registry.MustAddMetricSet("prometheus", "collector", New, mb.WithHostParser(hostParser))
}

type MetricSet struct {
	mb.BaseMetricSet
	httpClient *HTTPClient
	namespace  string
	actionOp   *actionOperator

	useTempFile     bool
	tempFilePattern string

	MetricReplace    map[string]string
	DimensionReplace map[string]string

	remoteClient           *http.Client
	workers                int
	disableCustomTimestamp bool
	normalizeMetricName    bool
	enableAlignTs          bool
	remoteRelabelCache     []*relabel.Config
	MetricRelabelRemote    string
	MetricRelabelConfigs   []*relabel.Config
}

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
		EnableAlignTs              bool              `config:"enable_align_ts"`
		DisableCustomTimestamp     bool              `config:"disable_custom_timestamp"`
		NormalizeMetricName        bool              `config:"normalize_metric_name"`
	}{}

	if err := base.Module().UnpackConfig(&config); err != nil {
		logger.Errorf("unpack failed, error: %s", err)
		return nil, err
	}
	logger.Infof("base.metric.set config: %+v", config)

	stdConfigs, actionConfigs, err := handleRelabels(config.MetricRelabelConfigs)
	if err != nil {
		logger.Errorf("handle relabels failed: %v", err)
		return nil, err
	}

	var relabels []*relabel.Config
	data, err := yaml.Marshal(stdConfigs)
	if err != nil {
		logger.Errorf("marshal metric relabel config failed: %s", err)
		return nil, err
	}

	if err = yaml.Unmarshal(data, &relabels); err != nil {
		logger.Errorf("unmarshal metric relabel config failed: %s", err)
		return nil, err
	}

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

	// 目前 delta/rate 为互斥，只能支持其一
	var actionOp *actionOperator
	if len(config.DiffMetrics) > 0 {
		actionOp = newActionOperator(ActionTypeDelta, nil, config.DiffMetrics)
	} else if len(actionConfigs.Rate) > 0 {
		actionOp = newActionOperator(ActionTypeRate, actionConfigs.Rate, nil)
	} else if len(actionConfigs.Delta) > 0 {
		actionOp = newActionOperator(ActionTypeDelta, nil, actionConfigs.Delta)
	}

	return &MetricSet{
		BaseMetricSet:          base,
		httpClient:             httpClient,
		namespace:              config.Namespace,
		actionOp:               actionOp,
		useTempFile:            utils.HasTempDir(),
		tempFilePattern:        config.TempFilePattern,
		remoteClient:           &http.Client{Timeout: duration},
		MetricRelabelRemote:    config.MetricRelabelRemote,
		MetricReplace:          config.MetricReplace,
		DimensionReplace:       config.DimensionReplace,
		MetricRelabelConfigs:   relabels,
		disableCustomTimestamp: config.DisableCustomTimestamp,
		normalizeMetricName:    config.NormalizeMetricName,
		workers:                config.Workers,
		enableAlignTs:          config.EnableAlignTs,
	}, nil
}

func (m *MetricSet) getEventFromPromEvent(promEvent *tasks.PromEvent) []common.MapStr {
	// 执行 relabels 规则
	if len(m.MetricRelabelConfigs) != 0 {
		if !m.metricRelabel(promEvent) {
			return nil
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

	if m.actionOp == nil {
		event["value"] = promEvent.Value
		return []common.MapStr{event}
	}

	newMetric, newValue, ok := m.actionOp.GetOrUpdate(promEvent.Key, promEvent.HashKey, promEvent.TS, promEvent.Value)
	if !ok {
		return nil
	}

	event["value"] = promEvent.Value

	// 不需要复制指标
	if newMetric == promEvent.Key {
		return []common.MapStr{event}
	}

	// 需要复制指标
	newEvent := event.Clone()
	newEvent["key"] = newMetric
	newEvent["value"] = newValue
	return []common.MapStr{event, newEvent}
}

// getEventsFromFile 从文件获取指标
func (m *MetricSet) getEventsFromFile(fileName string) (<-chan common.MapStr, error) {
	f, err := os.Open(fileName)
	if err != nil {
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

	var total atomic.Int64

	// 补充 up 指标文本
	markUp := func(failed bool, t0 time.Time) {
		// 需要减去自监控指标
		events := m.asEvents(CodeScrapeLine(int(total.Load()-2), m.logkvs()), milliTs)
		if failed {
			events = append(events, m.asEvents(CodeUp(define.CodeInvalidPromFormat, m.logkvs()), milliTs)...)
		} else {
			events = append(events, m.asEvents(CodeUp(define.CodeOK, m.logkvs()), milliTs)...)
		}
		events = append(events, m.asEvents(CodeHandleDuration(time.Since(t0).Seconds(), m.logkvs()), milliTs)...)
		for i := 0; i < len(events); i++ {
			eventChan <- events[i]
		}
	}

	start := time.Now()
	go func() {
		defer close(eventChan)
		defer cleanup()

		wg := sync.WaitGroup{}
		var produceErr atomic.Bool
		for i := 0; i < worker; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for line := range linesCh {
					events, err := m.produceEvents(line, milliTs)
					if err != nil {
						logger.Warnf("failed to produce events: %v", err)
						produceErr.Store(true)
						continue
					}
					for j := 0; j < len(events); j++ {
						eventChan <- events[j]
						total.Add(1)
					}
				}
			}()
		}
		wg.Wait()

		if up {
			markUp(produceErr.Load(), start) // 一次采集只上报一次状态
		}
	}()
	return eventChan
}

func normalizeName(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' }), "_")
}

func (m *MetricSet) asEvents(line string, timestamp int64) []common.MapStr {
	events, _ := m.produceEvents(line, timestamp)
	return events
}

func (m *MetricSet) produceEvents(line string, timestamp int64) ([]common.MapStr, error) {
	if len(line) <= 0 || line[0] == '#' {
		return nil, nil
	}

	timeOffset := 24 * time.Hour * 365 * 2 // 默认可容忍偏移时间为两年
	tsHandler, _ := tasks.GetTimestampHandler("s")
	promEvent, err := tasks.NewPromEvent(line, timestamp, timeOffset, tsHandler)
	if err != nil {
		return nil, errors.Wrapf(err, "parse line(%s) failed", line)
	}

	if m.normalizeMetricName {
		promEvent.Key = normalizeName(promEvent.Key)
	}

	// 生成事件
	events := m.getEventFromPromEvent(&promEvent)
	if len(events) == 0 {
		return nil, nil
	}

	var cloneEvents []common.MapStr
	for i := 0; i < len(events); i++ {
		event := events[i]
		// 基于配置进行指标复制
		targetMetricKey := m.getTargetMetricKey(&promEvent)
		if targetMetricKey != "" {
			targetEvent := event.Clone()
			targetEvent["key"] = targetMetricKey
			cloneEvents = append(cloneEvents, targetEvent)
		}
	}

	events = append(events, cloneEvents...)
	return events, nil
}

func (m *MetricSet) logkvs() []define.LogKV {
	return []define.LogKV{
		{K: "uri", V: m.HostData().SanitizedURI},
	}
}

// Fetch 采集逻辑入口
func (m *MetricSet) Fetch() (common.MapStr, error) {
	summary := common.MapStr{}
	startTime := time.Now()

	rsp, err := m.httpClient.FetchResponse()
	if err != nil {
		m.fillMetrics(summary, NewCodeReader(define.CodeConnRefused, m.logkvs()), false)
		err = errors.Wrap(err, "request failed")
		logger.Error(err)
		return summary, err
	}
	defer rsp.Body.Close()

	logger.Infof("http request: host=%s, take=%v", m.Host(), time.Since(startTime))

	var metricsFile *os.File
	if m.useTempFile {
		metricsFile, err = utils.CreateTempFile(m.tempFilePattern)
		if err != nil {
			m.fillMetrics(summary, NewCodeReader(define.CodeWriteTempFileFailed, m.logkvs()), false)
			err = errors.Wrap(err, "create metricsFile failed")
			logger.Error(err)
			return summary, err
		}

		if _, err = io.Copy(metricsFile, rsp.Body); err != nil {
			m.fillMetrics(summary, NewCodeReader(define.CodeWriteTempFileFailed, m.logkvs()), false)
			_ = metricsFile.Close()
			_ = os.Remove(metricsFile.Name())
			err = errors.Wrap(err, "write metricsFile failed")
			logger.Error(err)
			return summary, err
		}

		info, err := metricsFile.Stat()
		if err != nil {
			m.fillMetrics(summary, NewCodeReader(define.CodeWriteTempFileFailed, m.logkvs()), false)
			_ = metricsFile.Close()
			_ = os.Remove(metricsFile.Name())
			err = errors.Wrap(err, "stats metricsFile failed")
			logger.Error(err)
			return summary, err
		}

		metricsFile.WriteString("\n" + CodeScrapeSize(int(info.Size()), m.logkvs()))
		metricsFile.WriteString("\n" + CodeScrapeDuration(time.Since(startTime).Seconds(), m.logkvs()))

		if err = metricsFile.Close(); err != nil {
			m.fillMetrics(summary, NewCodeReader(define.CodeWriteTempFileFailed, m.logkvs()), false)
			_ = os.Remove(metricsFile.Name())
			err = errors.Wrap(err, "close metricsFile failed")
			logger.Error(err)
			return summary, err
		}
	}

	// 解析 prometheus 数据
	if m.useTempFile {
		summary["metrics_reader"] = define.MetricsReaderFunc(func() (<-chan common.MapStr, error) {
			return m.getEventsFromFile(metricsFile.Name())
		})
	} else {
		m.fillMetrics(summary, rsp.Body, true)
	}
	summary["namespace"] = m.namespace
	return summary, err
}

func (m *MetricSet) fillMetrics(summary common.MapStr, rc io.ReadCloser, up bool) {
	events := make([]common.MapStr, 0)
	for event := range m.getEventsFromReader(rc, func() {}, up) {
		events = append(events, event)
	}
	summary["metrics"] = events
}
