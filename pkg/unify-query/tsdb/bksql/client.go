// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bksql

import (
	"context"
	"encoding/json"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type Client struct {
	url     string
	headers map[string]string

	curl curl.Curl
}

func (c *Client) WithCurl(cc curl.Curl) *Client {
	c.curl = cc
	return c
}

func (c *Client) WithUrl(url string) *Client {
	c.url = url
	return c
}

func (c *Client) WithHeader(headers map[string]string) *Client {
	c.headers = headers
	return c
}

func (c *Client) curlGet(ctx context.Context, method, sql string, res *Result, span *trace.Span) error {
	if method == "" {
		method = curl.Post
	}
	params := &Params{}

	if sql != "" {
		params.SQL = sql
	}

	body, err := json.Marshal(params)
	if err != nil {
		return err
	}

	startAnaylize := time.Now()
	size, err := c.curl.Request(
		ctx, method,
		curl.Options{
			UrlPath: c.url,
			Body:    body,
			Headers: c.headers,
		},
		res,
	)
	if err != nil {
		return err
	}

	user := metadata.GetUser(ctx)
	metric.TsDBRequestBytes(ctx, size, user.SpaceUid, user.Source, consul.BkSqlStorageType)

	queryCost := time.Since(startAnaylize)
	if span != nil {
		span.Set("query-cost", queryCost.String())
	}

	metric.TsDBRequestSecond(
		ctx, queryCost, user.SpaceUid, consul.BkSqlStorageType,
	)
	return nil
}

func (c *Client) QuerySync(ctx context.Context, sql string, span *trace.Span) *Result {
	data := &QuerySyncResultData{}
	res := c.response(data)

	err := c.curlGet(ctx, curl.Post, sql, res, span)
	if err != nil {
		return c.failed(ctx, err)
	}

	return res
}

func (c *Client) response(data interface{}) *Result {
	return &Result{Data: data}
}

func (c *Client) failed(ctx context.Context, err error) *Result {
	return &Result{
		Result:  false,
		Message: err.Error(),
		Code:    StatusFailed,
	}
}
