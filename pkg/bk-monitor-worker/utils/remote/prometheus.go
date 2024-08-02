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
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/prompb"
)

type PrometheusStorageDataList []PrometheusStorageData

type PrometheusStorageData struct {
	Value []prompb.TimeSeries
}

type PrometheusWriterOption func(options *PrometheusWriterOptions)

type PrometheusWriterOptions struct {
	enabled bool
	url     string
	headers map[string]string
}

func PrometheusWriterEnabled(b bool) PrometheusWriterOption {
	return func(options *PrometheusWriterOptions) {
		options.enabled = b
	}
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
	config PrometheusWriterOptions

	client *http.Client
}

func GetPrometheusWriteOptions(opts ...PrometheusWriterOption) PrometheusWriterOptions {
	var res PrometheusWriterOptions
	for _, opt := range opts {
		opt(&res)
	}
	return res
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

func (p *PrometheusWriter) WriteBatch(tsList []prompb.TimeSeries) error {
	if !p.config.enabled {
		return nil
	}

	reqBytes, err := proto.Marshal(&prompb.WriteRequest{Timeseries: tsList})
	if err != nil {
		return err
	}
	compressedData := snappy.Encode(nil, reqBytes)
	req, err := http.NewRequest("POST", p.config.url, bytes.NewBuffer(compressedData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
	for k, v := range p.config.headers {
		req.Header.Set(k, v)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return errors.Errorf("[PromRemoteWrite] request failed: %s", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if resp.StatusCode >= 500 && resp.StatusCode < 600 {
		return fmt.Errorf("[PromRemoteWrite] remote write returned HTTP status %v; err = %w: %s", resp.Status, err, body)
	}

	return nil
}

func NewPrometheusWriterClient(config PrometheusWriterOptions) *PrometheusWriter {
	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 5,
		},
		Timeout: 10 * time.Second,
	}

	return &PrometheusWriter{
		config: config,
		client: client,
	}
}
