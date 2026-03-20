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
	"math"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/remote"
	monitorLogger "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	PromRelationMetric = iota
	PromFlowMetric
)

type PrometheusStorageData struct {
	Kind int
	// Kind -> Relation Value -> []string
	// Kind -> Flow Value -> map[string]FlowMetricRecordStats
	Value any
}

type MetricConfigOption func(options *MetricConfigOptions)

type MetricConfigOptions struct {
	relationMetricMemDuration time.Duration
	flowMetricMemDuration     time.Duration
	flowMetricBuckets         []float64
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

type MetricDimensionsHandler struct {
	dataId string

	ctx context.Context
	mu  sync.Mutex

	relationMetricDimensions *relationMetricsCollector
	flowMetricCollector      *flowMetricsCollector

	promClient       *remote.PrometheusWriter
	relationReporter remote.Reporter
	relationSpaceUID string
	logger           monitorLogger.Logger
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

func (m *MetricDimensionsHandler) cleanUpAndReport(kind int, c MetricCollector) {
	m.mu.Lock()
	defer m.mu.Unlock()

	writeReq := c.Collect()
	metrics.RecordApmPreCalcOperateStorageCount(m.dataId, metrics.StoragePrometheus, metrics.OperateSave)
	metrics.RecordApmPreCalcSaveStorageTotal(m.dataId, metrics.StoragePrometheus, len(writeReq.Timeseries))
	if len(writeReq.Timeseries) == 0 {
		return
	}

	if kind == PromRelationMetric && m.relationReporter != nil && m.relationSpaceUID != "" {
		if err := m.relationReporter.Do(context.Background(), m.relationSpaceUID, writeReq.Timeseries...); err != nil {
			metrics.RecordApmPreCalcOperateStorageFailedTotal(m.dataId, metrics.SavePrometheusFailed)
			m.logger.Errorf("[TraceMetricsReport] DataId: %s report relation metrics to centralized space(%s) failed, error: %s", m.dataId, m.relationSpaceUID, err)
		}
		return
	}

	if err := m.promClient.WriteBatch(context.Background(), "", writeReq); err != nil {
		metrics.RecordApmPreCalcOperateStorageFailedTotal(m.dataId, metrics.SavePrometheusFailed)
		m.logger.Errorf("[TraceMetricsReport] DataId: %s report to prometheus failed, error: %s", m.dataId, err)
	}
}

func (m *MetricDimensionsHandler) LoopCollect(kind int, c MetricCollector) {
	ticker := time.NewTicker(c.Ttl())
	m.logger.Infof("[MetricReport] start loop, listen for metrics, interval: %s", c.Ttl())

	for {
		select {
		case <-ticker.C:
			m.cleanUpAndReport(kind, c)
		case <-m.ctx.Done():
			ticker.Stop()
			m.logger.Infof("[MetricReport] stop report metrics")
			return
		}
	}
}

func (m *MetricDimensionsHandler) Close() {
	m.cleanUpAndReport(PromRelationMetric, m.relationMetricDimensions)
	m.cleanUpAndReport(PromFlowMetric, m.flowMetricCollector)
}

func NewMetricDimensionHandler(ctx context.Context, dataId string,
	writerConfig remote.PrometheusWriterOptions,
	metricsConfig MetricConfigOptions,
) *MetricDimensionsHandler {
	token := core.GetMetadataCenter().GetToken(dataId)
	bkBizId := core.GetMetadataCenter().GetBaseInfo(dataId).BkBizId
	relationSpaceUID := ""
	if bkBizId != "" {
		relationSpaceUID = "bkcc__" + bkBizId
	}
	relationReporter, _ := remote.NewSpaceReporter(config.BuildInResultTableDetailKey, writerConfig.Url)
	relationMetricRoute := "apm_dataid_token"

	if relationReporter != nil && relationSpaceUID != "" {
		relationMetricRoute = relationSpaceUID
	}
	monitorLogger.Infof(
		"[MetricDimension] \ncreate metric handler\n====\n"+
			"prometheus host: %s \nconfigHeaders: %v \ndataId(%s) -> token: %s \nrelationMetricRoute: %s \n"+
			"flowMetricDuration: %s \nflowMetricBucket: %v \nrelationMetricDuration: %s \n====\n",
		writerConfig.Url, writerConfig.Headers, dataId, token, relationMetricRoute,
		metricsConfig.flowMetricMemDuration, metricsConfig.flowMetricBuckets, metricsConfig.relationMetricMemDuration,
	)

	h := &MetricDimensionsHandler{
		dataId:                   dataId,
		promClient:               remote.NewPrometheusWriterClient(token, writerConfig.Url, writerConfig.Headers),
		relationReporter:         relationReporter,
		relationSpaceUID:         relationSpaceUID,
		relationMetricDimensions: newRelationMetricCollector(metricsConfig.relationMetricMemDuration),
		flowMetricCollector:      newFlowMetricCollector(metricsConfig.flowMetricBuckets, metricsConfig.flowMetricMemDuration),
		ctx:                      ctx,
		logger:                   monitorLogger.With(zap.String("name", "metricHandler"), zap.String("dataId", dataId)),
	}
	go h.LoopCollect(PromRelationMetric, h.relationMetricDimensions)
	go h.LoopCollect(PromFlowMetric, h.flowMetricCollector)
	return h
}
