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
	"net/http"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/prompb"
	"go.uber.org/zap"
	"golang.org/x/exp/maps"

	monitorLogger "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	tokenKey = "X-BK-TOKEN"
)

type PrometheusWriterOption func(options *PrometheusWriterOptions)

type PrometheusWriterOptions struct {
	Url     string
	Headers map[string]string
}

func PrometheusWriterUrl(u string) PrometheusWriterOption {
	return func(options *PrometheusWriterOptions) {
		options.Url = u
	}
}

func PrometheusWriterHeaders(h map[string]string) PrometheusWriterOption {
	return func(options *PrometheusWriterOptions) {
		options.Headers = h
	}
}

type PrometheusWriter struct {
	url     string
	headers map[string]string

	client       *http.Client
	logger       monitorLogger.Logger
	responseHook func(bool)
}

func (p *PrometheusWriter) Close(_ context.Context) error {
	if p.client != nil {
		p.client.CloseIdleConnections()
		p.client = nil
	}
	return nil
}

func (p *PrometheusWriter) WriteBatch(ctx context.Context, token string, writeReq prompb.WriteRequest) error {
	if len(writeReq.Timeseries) == 0 {
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

	if req.Header.Get(tokenKey) == "" {
		return nil
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

	p.logger.Infof("[RemoteWrite] push %d series to host: %s (Headers: %+v))", len(writeReq.Timeseries), p.url, p.headers)

	return nil
}

func NewPrometheusWriterClient(token, url string, headers map[string]string) *PrometheusWriter {
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
		if _, oExist := h[tokenKey]; !oExist {
			h[tokenKey] = token
		}
	} else {
		h[tokenKey] = h["x-bk-token"]
	}

	return &PrometheusWriter{
		url:     url,
		headers: h,
		client:  client,
		logger:  monitorLogger.With(zap.String("name", "prometheus")),
	}
}
