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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
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

type prometheusWriter struct {
	dataId  string
	url     string
	headers map[string]string

	client  *http.Client
	isValid bool
}

func (p *prometheusWriter) WriteBatch(writeReq prompb.WriteRequest) error {
	if !p.isValid || len(writeReq.Timeseries) == 0 {
		return nil
	}

	// TODO 补充指标的元数据信息
	reqBytes, err := proto.Marshal(&writeReq)
	if err != nil {
		return err
	}
	compressedData := snappy.Encode(nil, reqBytes)
	req, err := http.NewRequest("POST", p.url, bytes.NewBuffer(compressedData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")

	for k, v := range p.headers {
		req.Header.Set(k, v)
	}

	metrics.RecordApmPreCalcOperateStorageCount(p.dataId, metrics.StoragePrometheus, metrics.OperateSave)
	metrics.RecordApmPreCalcSaveStorageTotal(p.dataId, metrics.StoragePrometheus, len(writeReq.Timeseries))
	resp, err := p.client.Do(req)

	logger.Infof("[RemoteWrite] push %d series", len(writeReq.Timeseries))
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

	return nil
}

func newPrometheusWriterClient(dataId, token, url string, headers map[string]string) *prometheusWriter {
	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 5,
		},
		Timeout: 10 * time.Second,
	}

	isValid := false
	if _, exist := headers["x-bk-token"]; !exist {
		if token == "" {
			logger.Errorf("[PromeRemoteWrite] token is empty! Metrics will not be report!")
		} else {
			headers["X-BK-TOKEN"] = token
			isValid = true
		}
	} else {
		isValid = true
	}

	return &prometheusWriter{
		dataId:  dataId,
		url:     url,
		headers: headers,
		client:  client,
		isValid: isValid,
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
		logger.Warnf("[MetricDimensionHandler] receive not support kind: %d", data.Kind)
	}
}

func (m *MetricDimensionsHandler) cleanUpAndReport(c MetricCollector) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.promClient.WriteBatch(c.Collect()); err != nil {
		logger.Errorf("[TraceMetricsReport] report to %s failed, error: %s", m.promClient.url, err)
	}
}

func (m *MetricDimensionsHandler) LoopCollect(c MetricCollector) {
	ticker := time.NewTicker(c.Ttl())
	logger.Infof("[MetricReport] start loop, listen for metrics, interval: %s", c.Ttl())

	for {
		select {
		case <-ticker.C:
			m.cleanUpAndReport(c)
		case <-m.ctx.Done():
			ticker.Stop()
			logger.Infof("[MetricReport] stop report metrics")
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
	logger.Infof(
		"[MetricDimension] \ncreate metric handler\n====\n"+
			"prometheus host: %s \nheaders: %s \ndataId(%s) -> token: %s \n"+
			"flowMetricDuration: %s flowMetricBucket: %v \nrelationMetricDuration: %s \n====\n",
		config.url, config.headers, dataId, token,
		metricsConfig.flowMetricMemDuration, metricsConfig.flowMetricBuckets, metricsConfig.relationMetricMemDuration,
	)

	h := &MetricDimensionsHandler{
		promClient:               newPrometheusWriterClient(dataId, core.GetMetadataCenter().GetToken(dataId), config.url, config.headers),
		relationMetricDimensions: newRelationMetricCollector(metricsConfig.relationMetricMemDuration),
		flowMetricCollector:      newFlowMetricCollector(metricsConfig.flowMetricBuckets, metricsConfig.flowMetricMemDuration),
		ctx:                      ctx,
	}
	go h.LoopCollect(h.relationMetricDimensions)
	go h.LoopCollect(h.flowMetricCollector)
	return h
}
