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
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/prompb"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
)

type PrometheusStorageData struct {
	Value []string
}

type PrometheusWriterOption func(options *PrometheusWriterOptions)

type PrometheusWriterOptions struct {
	url     string
	headers map[string]string
	ttl     time.Duration
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

func PrometheusWriterReportInterval(interval time.Duration) PrometheusWriterOption {
	return func(options *PrometheusWriterOptions) {
		options.ttl = interval
	}
}

type prometheusWriter struct {
	dataId  string
	url     string
	headers map[string]string

	client  *http.Client
	isValid bool
}

func (p *prometheusWriter) WriteBatch(series []prompb.TimeSeries) error {
	if !p.isValid || len(series) == 0 {
		return nil
	}

	reqBytes, err := proto.Marshal(&prompb.WriteRequest{Timeseries: series})
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
	metrics.RecordApmPreCalcSaveStorageTotal(p.dataId, metrics.StoragePrometheus, len(series))
	resp, err := p.client.Do(req)

	logger.Infof("[RemoteWrite] push %d series", len(series))
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

type MetricDimensionsHandler struct {
	mu         sync.Mutex
	dimensions map[string]time.Time
	ttl        time.Duration
	promClient *prometheusWriter

	ctx context.Context
}

func (m *MetricDimensionsHandler) AddBatch(labels []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range labels {
		if _, exist := m.dimensions[s]; !exist {
			m.dimensions[s] = time.Now()
		}
	}
}

func (m *MetricDimensionsHandler) cleanUpAndReport() {
	m.mu.Lock()
	defer m.mu.Unlock()
	edge := time.Now().Add(-m.ttl)
	var keys []string
	for dimensionKey, ts := range m.dimensions {
		if ts.Before(edge) {
			keys = append(keys, dimensionKey)
		}
	}
	series := m.convert(keys)
	for _, k := range keys {
		delete(m.dimensions, k)
	}
	if err := m.promClient.WriteBatch(series); err != nil {
		logger.Errorf("[TraceMetricsReport] report to %s failed, error: %s", m.promClient.url, err)
	}
}

func (m *MetricDimensionsHandler) convert(dimensionKeys []string) []prompb.TimeSeries {
	var res []prompb.TimeSeries
	ts := time.Now().UnixNano() / int64(time.Millisecond)
	for _, key := range dimensionKeys {
		pairs := strings.Split(key, ",")
		var labels []prompb.Label
		for _, pair := range pairs {
			composition := strings.Split(pair, "=")
			if len(composition) == 2 {
				labels = append(labels, prompb.Label{Name: composition[0], Value: composition[1]})
			}
		}

		res = append(res, prompb.TimeSeries{
			Labels:  labels,
			Samples: []prompb.Sample{{Value: 1, Timestamp: ts}},
		})
	}

	return res
}

func (m *MetricDimensionsHandler) Loop() {
	ticker := time.NewTicker(m.ttl)
	logger.Infof("[MetricReport] start loop, listen for traceMetrics, interval: %s", m.ttl)

	for {
		select {
		case <-ticker.C:
			m.cleanUpAndReport()
		case <-m.ctx.Done():
			ticker.Stop()
			logger.Infof("[MetricReport] stop report metrics")
			return
		}
	}
}

func (m *MetricDimensionsHandler) Close() {
	m.cleanUpAndReport()
}

func NewMetricDimensionHandler(ctx context.Context, dataId string, config PrometheusWriterOptions) *MetricDimensionsHandler {

	h := &MetricDimensionsHandler{
		ttl:        config.ttl,
		promClient: newPrometheusWriterClient(dataId, core.GetMetadataCenter().GetToken(dataId), config.url, config.headers),
		dimensions: make(map[string]time.Time),
		ctx:        ctx,
	}
	go h.Loop()
	return h
}
