// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/prometheus/prompb"
	"go.uber.org/zap"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/remote"
	monitorLogger "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

const (
	PromRelationMetric = iota
	PromFlowMetric
)

type PrometheusStorageData struct {
	AppKey core.AppKey
	Kind   int
	// Kind -> Relation Value -> []string
	// Kind -> Flow Value -> map[string]FlowMetricRecordStats
	Value any
}

type MetricConfigOption func(options *MetricConfigOptions)

type MetricConfigOptions struct {
	relationMetricMemDuration  time.Duration
	flowMetricMemDuration      time.Duration
	flowMetricBuckets          []float64
	builtinRelationEnabled     bool
	builtinRelationBizIDs      map[string]struct{}
	builtinRelationDetailKey   string
	relationDefinitionProvider relationDefinitionProvider
	relationRouteProvider      RelationRouteProvider
}

func MetricRelationMemDuration(m time.Duration) MetricConfigOption {
	return func(options *MetricConfigOptions) {
		options.relationMetricMemDuration = m
	}
}

func MetricFlowMemDuration(m time.Duration) MetricConfigOption {
	return func(options *MetricConfigOptions) {
		options.flowMetricMemDuration = m
	}
}

func MetricFlowBuckets(b []float64) MetricConfigOption {
	return func(options *MetricConfigOptions) {
		sort.Float64s(b)
		res := make([]float64, 0, len(b)+1)
		for i := 0; i < len(b); i++ {
			res = append(res, b[i]*1e6)
		}
		res = append(res, math.MaxFloat64)
		options.flowMetricBuckets = res
	}
}

func MetricBuiltinRelationReport(enabled bool, bkBizIDs []string, resultTableDetailKey string) MetricConfigOption {
	return func(options *MetricConfigOptions) {
		options.builtinRelationEnabled = enabled
		options.builtinRelationBizIDs = make(map[string]struct{}, len(bkBizIDs))
		for _, bkBizID := range bkBizIDs {
			options.builtinRelationBizIDs[bkBizID] = struct{}{}
		}
		options.builtinRelationDetailKey = resultTableDetailKey
	}
}

func MetricRelationDefinitionProvider(provider relationDefinitionProvider) MetricConfigOption {
	return func(options *MetricConfigOptions) {
		options.relationDefinitionProvider = provider
	}
}

func MetricRelationRouteProvider(provider RelationRouteProvider) MetricConfigOption {
	return func(options *MetricConfigOptions) {
		options.relationRouteProvider = provider
	}
}

func (m MetricConfigOptions) builtinRelationEnabledForBiz(bkBizID string) bool {
	if !m.builtinRelationEnabled {
		return false
	}
	_, ok := m.builtinRelationBizIDs[bkBizID]
	return ok
}

type prometheusWriter interface {
	WriteBatch(ctx context.Context, token string, writeReq prompb.WriteRequest) error
	Close(ctx context.Context) error
}

type relationDefinitionProvider interface {
	ListRelationDefinitions(namespace string) ([]*relation.RelationDefinition, error)
}

type RelationRouteProvider interface {
	Ready(spaceUID string) bool
}

type MetricDimensionsHandler struct {
	dataId string

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	wg     sync.WaitGroup

	relationMetricDimensions *relationMetricsCollector
	flowMetricCollector      *flowMetricsCollector

	promClient                 prometheusWriter
	builtinRelationReporter    remote.Reporter
	builtinRelationSpaceUID    string
	relationDefinitionProvider relationDefinitionProvider
	relationRouteProvider      RelationRouteProvider
	logger                     monitorLogger.Logger
}

var prometheusHistogramSuffixes = []string{"_bucket", "_sum", "_count", "_min", "_max"}

func getTimeSeriesMetricName(ts prompb.TimeSeries) string {
	for _, label := range ts.Labels {
		if label.Name == "__name__" {
			return label.Value
		}
	}
	return ""
}

func matchRelationMetricName(metricName string, relationMetricNames map[string]struct{}) bool {
	if _, ok := relationMetricNames[metricName]; ok {
		return true
	}
	for _, suffix := range prometheusHistogramSuffixes {
		if strings.HasSuffix(metricName, suffix) {
			_, ok := relationMetricNames[strings.TrimSuffix(metricName, suffix)]
			return ok
		}
	}
	return false
}

func filterRelationTimeseries(
	provider relationDefinitionProvider, namespace string, timeseries []prompb.TimeSeries,
) ([]prompb.TimeSeries, error) {
	definitions, err := provider.ListRelationDefinitions(namespace)
	if err != nil {
		return nil, err
	}
	relationMetricNames := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		if definition == nil {
			continue
		}
		relationMetricNames[definition.GetRelationName()] = struct{}{}
	}

	filtered := make([]prompb.TimeSeries, 0, len(timeseries))
	for _, ts := range timeseries {
		if matchRelationMetricName(getTimeSeriesMetricName(ts), relationMetricNames) {
			filtered = append(filtered, ts)
		}
	}
	return filtered, nil
}

func (m *MetricDimensionsHandler) Add(data PrometheusStorageData) {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch data.Kind {
	case PromRelationMetric:
		m.relationMetricDimensions.Observe(data.Value)
	case PromFlowMetric:
		m.flowMetricCollector.Observe(data.Value)
	default:
		m.logger.Warnf("[MetricDimensionHandler] receive not support kind: %d", data.Kind)
	}
}

func (m *MetricDimensionsHandler) cleanUpAndReport(c MetricCollector) {
	m.mu.Lock()
	defer m.mu.Unlock()

	writeReq := c.Collect()
	metrics.RecordApmPreCalcOperateStorageCount(m.dataId, metrics.StoragePrometheus, metrics.OperateSave)
	metrics.RecordApmPreCalcSaveStorageTotal(m.dataId, metrics.StoragePrometheus, len(writeReq.Timeseries))
	if len(writeReq.Timeseries) == 0 {
		return
	}
	if err := m.promClient.WriteBatch(context.Background(), "", writeReq); err != nil {
		metrics.RecordApmPreCalcOperateStorageFailedTotal(m.dataId, metrics.SavePrometheusFailed)
		m.logger.Errorf("[TraceMetricsReport] DataId: %s report to prometheus failed, error: %s", m.dataId, err)
	}
	if m.builtinRelationReporter == nil {
		return
	}
	if m.relationRouteProvider != nil && !m.relationRouteProvider.Ready(m.builtinRelationSpaceUID) {
		return
	}
	builtinRelationTimeseries, err := filterRelationTimeseries(
		m.relationDefinitionProvider, m.builtinRelationSpaceUID, writeReq.Timeseries,
	)
	if err != nil {
		metrics.RecordApmPreCalcOperateStorageFailedTotal(m.dataId, metrics.SavePrometheusFailed)
		m.logger.Errorf(
			"[TraceMetricsReport] DataId: %s match relation definitions failed, spaceUID: %s, error: %s",
			m.dataId, m.builtinRelationSpaceUID, err,
		)
		return
	}
	if len(builtinRelationTimeseries) == 0 {
		return
	}
	if err := m.builtinRelationReporter.Do(
		context.Background(), m.builtinRelationSpaceUID, builtinRelationTimeseries...,
	); err != nil {
		metrics.RecordApmPreCalcOperateStorageFailedTotal(m.dataId, metrics.SavePrometheusFailed)
		m.logger.Errorf(
			"[TraceMetricsReport] DataId: %s dual-write to built-in relation failed, spaceUID: %s, error: %s",
			m.dataId, m.builtinRelationSpaceUID, err,
		)
	}
}

func (m *MetricDimensionsHandler) LoopCollect(c MetricCollector) {
	defer m.wg.Done()

	ticker := time.NewTicker(c.Ttl())
	m.logger.Infof("[MetricReport] start loop, listen for metrics, interval: %s", c.Ttl())

	for {
		select {
		case <-ticker.C:
			m.cleanUpAndReport(c)
		case <-m.ctx.Done():
			ticker.Stop()
			m.logger.Infof("[MetricReport] stop report metrics")
			return
		}
	}
}

func (m *MetricDimensionsHandler) Close() {
	m.cancel()
	m.wg.Wait()
	m.cleanUpAndReport(m.relationMetricDimensions)
	m.cleanUpAndReport(m.flowMetricCollector)
	if m.builtinRelationReporter != nil {
		if err := m.builtinRelationReporter.Close(context.Background()); err != nil {
			m.logger.Errorf(
				"[TraceMetricsReport] DataId: %s close built-in relation reporter failed, error: %s", m.dataId, err,
			)
		}
	}
	if err := m.promClient.Close(context.Background()); err != nil {
		m.logger.Errorf("[TraceMetricsReport] DataId: %s close prometheus writer failed, error: %s", m.dataId, err)
	}
}

func NewMetricDimensionHandler(
	ctx context.Context,
	dataId string,
	baseInfo core.BaseInfo,
	config remote.PrometheusWriterOptions,
	metricsConfig MetricConfigOptions,
) *MetricDimensionsHandler {
	handlerCtx, cancel := context.WithCancel(ctx)
	var (
		builtinRelationReporter remote.Reporter
		builtinRelationSpaceUID string
	)
	if metricsConfig.builtinRelationEnabledForBiz(baseInfo.BkBizId) &&
		metricsConfig.relationDefinitionProvider != nil && metricsConfig.relationRouteProvider != nil {
		reporter, err := remote.NewSpaceReporter(metricsConfig.builtinRelationDetailKey, config.Url)
		if err != nil {
			monitorLogger.Errorf(
				"[MetricDimension] DataId(%s) appKey(%+v) create built-in relation reporter failed: %s",
				dataId, baseInfo.AppKey(), err,
			)
		} else {
			builtinRelationReporter = reporter
			builtinRelationSpaceUID = fmt.Sprintf("bkcc__%s", baseInfo.BkBizId)
		}
	}

	monitorLogger.Infof(
		"[MetricDimension] \ncreate metric handler\n====\n"+
			"prometheus host: %s \nconfigHeaders: %s \ndataId(%s) appKey(%+v) -> token: %s \n"+
			"flowMetricDuration: %s \nflowMetricBucket: %v \nrelationMetricDuration: %s \n====\n",
		config.Url, config.Headers, dataId, baseInfo.AppKey(), baseInfo.Token,
		metricsConfig.flowMetricMemDuration, metricsConfig.flowMetricBuckets, metricsConfig.relationMetricMemDuration,
	)

	h := &MetricDimensionsHandler{
		dataId:                     dataId,
		promClient:                 remote.NewPrometheusWriterClient(baseInfo.Token, config.Url, config.Headers),
		builtinRelationReporter:    builtinRelationReporter,
		builtinRelationSpaceUID:    builtinRelationSpaceUID,
		relationDefinitionProvider: metricsConfig.relationDefinitionProvider,
		relationRouteProvider:      metricsConfig.relationRouteProvider,
		relationMetricDimensions:   newRelationMetricCollector(metricsConfig.relationMetricMemDuration),
		flowMetricCollector:        newFlowMetricCollector(metricsConfig.flowMetricBuckets, metricsConfig.flowMetricMemDuration),
		ctx:                        handlerCtx,
		cancel:                     cancel,
		logger:                     monitorLogger.With(zap.String("name", "metricHandler"), zap.String("dataId", dataId)),
	}
	h.wg.Add(2)
	go h.LoopCollect(h.relationMetricDimensions)
	go h.LoopCollect(h.flowMetricCollector)
	return h
}
