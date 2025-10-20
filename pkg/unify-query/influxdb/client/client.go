// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	_ "github.com/influxdata/influxdb1-client" // this is important because of the bug in go mod
	"github.com/influxdata/influxdb1-client/models"
	influxclient "github.com/influxdata/influxdb1-client/v2"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type Client interface {
	Query(ctx context.Context, db, sql, precision, contentType string, chunked bool) (*decoder.Response, error)
}

// BasicClient
type BasicClient struct {
	client      *http.Client
	address     string
	username    string
	password    string
	contentType string
	chunkSize   int
}

// NewBasicClient
func NewBasicClient(address, username, password, contentType string, chunkSize int) Client {
	return &BasicClient{
		client:      http.DefaultClient,
		address:     address,
		username:    username,
		password:    password,
		contentType: contentType,
		chunkSize:   chunkSize,
	}
}

// Query
func (c *BasicClient) Query(
	ctx context.Context, db, sql, precision, contentType string, chunked bool,
) (*decoder.Response, error) {
	var err error

	ctx, span := trace.NewSpan(ctx, "influxdb-client-query")
	defer span.End(&err)

	values := &url.Values{}
	values.Set("db", db)
	values.Set("q", sql)
	values.Set("precision", precision)
	if chunked {
		values.Set("chunked", "true")
		values.Set("chunk_size", fmt.Sprintf("%d", c.chunkSize))
	}

	urlPath := fmt.Sprintf("%s/query?%s", c.address, values.Encode())

	span.Set("query-params", values.Encode())
	span.Set("http-url", urlPath)

	client := http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	req, err := http.NewRequestWithContext(ctx, "GET", urlPath, nil)
	if err != nil {
		return nil, metadata.Sprintf(
			metadata.MsgQueryInfluxDB,
			"创建HTTP %s 请求失败",
			urlPath,
		).Error(ctx, err)
	}
	req.SetBasicAuth(c.username, c.password)
	usingContentType := contentType
	if contentType == "" {
		usingContentType = c.contentType
	}

	// chunk 模式下只支持json
	req.Header.Set("accept", usingContentType)
	span.Set("http-request", req.Header)

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return nil, metadata.Sprintf(
			metadata.MsgQueryInfluxDB,
			"%s 请求失败",
			urlPath,
		).Error(ctx, err)
	}
	defer func() {
		err = resp.Body.Close()
		if err != nil {
			metadata.Sprintf(
				metadata.MsgQueryInfluxDB,
				"%s 请求失败 %v",
				urlPath, err,
			).Warn(ctx)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, metadata.Sprintf(
			metadata.MsgQueryInfluxDB,
			"查询失败",
		).Error(ctx, errors.New(resp.Status))
	}

	respContentType := resp.Header.Get("Content-type")

	var result *decoder.Response
	result, err = c.decodeWithContentType(ctx, respContentType, resp)
	span.Set("query-cost", time.Since(start))

	return result, err
}

// decodeWithContentType:
func (c *BasicClient) decodeWithContentType(
	ctx context.Context, respContentType string, resp *http.Response,
) (*decoder.Response, error) {
	dec, err := decoder.GetDecoder(respContentType)
	if err != nil {
		data, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, metadata.Sprintf(
				metadata.MsgQueryInfluxDB,
				"%s 解码失败",
				respContentType,
			).Error(ctx, errors.New(resp.Status))
		}
		return nil, metadata.Sprintf(
			metadata.MsgQueryInfluxDB,
			"%s 解码失败 %v",
			respContentType, string(data),
		).Error(ctx, err)
	}
	result := new(decoder.Response)
	_, err = dec.Decode(ctx, resp.Body, result)
	if err != nil {
		return nil, metadata.Sprintf(
			metadata.MsgQueryInfluxDB,
			"%s 解码失败",
			respContentType,
		).Error(ctx, err)
	}

	return result, nil
}

// Result
type Result struct {
	SeriesMap map[uint64]models.Row // {hashkey: row}
	Messages  []*influxclient.Message
	Err       string `json:"error,omitempty"`
}
