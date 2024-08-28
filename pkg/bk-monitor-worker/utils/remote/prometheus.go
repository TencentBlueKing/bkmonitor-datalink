// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package remote

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/prompb"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"go.uber.org/zap"
	"golang.org/x/exp/maps"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	monitorLogger "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	PromRelationMetric = iota
	PromFlowMetric
)

const (
	tokenKey = "X-BK-TOKEN"
)

type PrometheusStorageDataList []PrometheusStorageData

type PrometheusStorageData struct {
	Kind int
	// Kind -> Relation Value -> []string
	// Kind -> Flow Value -> map[string]FlowMetricRecordStats
	Value any
}

type PrometheusWriterOption func(options *PrometheusWriterOptions)

type PrometheusWriterOptions struct {
	url     string
	headers map[string]string
}

func PrometheusWriterUrl(u string) PrometheusWriterOption {
	return func(options *PrometheusWriterOptions) {
		options.url = u
	}
}

func PrometheusWriterHeaders(h map[string]string) PrometheusWriterOption {
	return func(options *PrometheusWriterOptions) {
		options.headers = h
	}
}

type PrometheusWriter struct {
	dataId  string
	url     string
	headers map[string]string

	client  *http.Client
	isValid bool
	logger  monitorLogger.Logger
}

func (d PrometheusStorageDataList) ToTimeSeries() []prompb.TimeSeries {
	if d == nil {
		return nil
	}
	var ts []prompb.TimeSeries
	for _, item := range d {
		ts = append(ts, item.Value...)
	}
	return ts
}

func (p *PrometheusWriter) WriteBatch(ctx context.Context, token string, tsList []prompb.TimeSeries) error {
	if !p.isValid || len(writeReq.Timeseries) == 0 {
		return nil
	}

	// TODO 补充指标的元数据信息
	reqBytes, err := proto.Marshal(&writeReq)
	if err != nil {
		return err
	}
	compressedData := snappy.Encode(nil, reqBytes)
	req, err := http.NewRequestWithContext(ctx, "POST", p.url, bytes.NewBuffer(compressedData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")

	for k, v := range p.headers {
		req.Header.Set(k, v)
	}

	// 支持使用不同的 token
	if token != "" {
		req.Header.Set(tokenKey, token)
	}

	metrics.RecordApmPreCalcOperateStorageCount(p.dataId, metrics.StoragePrometheus, metrics.OperateSave)
	metrics.RecordApmPreCalcSaveStorageTotal(p.dataId, metrics.StoragePrometheus, len(writeReq.Timeseries))
	resp, err := p.client.Do(req)

	p.logger.Debugf("[RemoteWrite] push %d series to host: %s (headers: %+v))", len(writeReq.Timeseries), p.url, p.headers)
	if err != nil {
		metrics.RecordApmPreCalcOperateStorageFailedTotal(p.dataId, metrics.SavePrometheusFailed)
		return errors.Errorf("[PromRemoteWrite] request failed: %s", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if resp.StatusCode >= 500 && resp.StatusCode < 600 {
		metrics.RecordApmPreCalcOperateStorageFailedTotal(p.dataId, metrics.SavePrometheusFailed)
		return fmt.Errorf("[PromRemoteWrite] remote write returned HTTP status %v; err = %w: %s", resp.Status, err, body)
	}

	logger.Infof("prom remote wirte ts: %d", len(tsList))

	return nil
}

func NewPrometheusWriterClient(dataId, token, url string, headers map[string]string) *prometheusWriter {
	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 5,
		},
		Timeout: 10 * time.Second,
	}

	h := make(map[string]string, len(headers))
	maps.Copy(h, headers)
	if _, exist := h["x-bk-token"]; !exist {
		if _, oExist := h["X-BK-TOKEN"]; !oExist {
			h["X-BK-TOKEN"] = token
		}
	} else {
		h["X-BK-TOKEN"] = h["x-bk-token"]
	}
	isValid := false
	if v, _ := h["X-BK-TOKEN"]; v != "" {
		isValid = true
	}

	return &prometheusWriter{
		dataId:  dataId,
		url:     url,
		headers: h,
		client:  client,
		isValid: isValid,
		logger:  monitorLogger.With(zap.String("name", "prometheus"), zap.String("dataId", dataId)),
	}
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
	ctx context.Context
	mu  sync.Mutex

	relationMetricDimensions *relationMetricsCollector
	flowMetricCollector      *flowMetricsCollector

	promClient *prometheusWriter
	logger     monitorLogger.Logger
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

	if err := m.promClient.WriteBatch(c.Collect()); err != nil {
		m.logger.Errorf("[TraceMetricsReport] report to %s failed, error: %s", m.promClient.url, err)
	}
}

func (m *MetricDimensionsHandler) LoopCollect(c MetricCollector) {
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
	m.cleanUpAndReport(m.relationMetricDimensions)
	m.cleanUpAndReport(m.flowMetricCollector)
}

func NewMetricDimensionHandler(ctx context.Context, dataId string,
	config PrometheusWriterOptions,
	metricsConfig MetricConfigOptions,
) *MetricDimensionsHandler {

	token := core.GetMetadataCenter().GetToken(dataId)
	monitorLogger.Infof(
		"[MetricDimension] \ncreate metric handler\n====\n"+
			"prometheus host: %s \nconfigHeaders: %s \ndataId(%s) -> token: %s \n"+
			"flowMetricDuration: %s \nflowMetricBucket: %v \nrelationMetricDuration: %s \n====\n",
		config.url, config.headers, dataId, token,
		metricsConfig.flowMetricMemDuration, metricsConfig.flowMetricBuckets, metricsConfig.relationMetricMemDuration,
	)

	h := &MetricDimensionsHandler{
		promClient:               newPrometheusWriterClient(dataId, token, config.url, config.headers),
		relationMetricDimensions: newRelationMetricCollector(metricsConfig.relationMetricMemDuration),
		flowMetricCollector:      newFlowMetricCollector(metricsConfig.flowMetricBuckets, metricsConfig.flowMetricMemDuration),
		ctx:                      ctx,
		logger:                   monitorLogger.With(zap.String("name", "metricHandler"), zap.String("dataId", dataId)),
	}
	go h.LoopCollect(h.relationMetricDimensions)
	go h.LoopCollect(h.flowMetricCollector)
	return h
}
