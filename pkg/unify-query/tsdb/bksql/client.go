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
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
)

type Client struct {
	Address string

	BkdataAuthenticationMethod string
	BkUsername                 string
	BkAppCode                  string

	// 不传这个值，bk-sql 会自动筛选
	PreferStorage string

	BkdataDataToken string
	BkAppSecret     string

	ContentType string

	Log *log.Logger

	Timeout time.Duration
	Curl    curl.Curl
}

func (c *Client) curl(ctx context.Context, method, url, sql string, res *Result) error {
	if method == "" {
		method = curl.Post
	}
	params := &Params{
		BkdataAuthenticationMethod: c.BkdataAuthenticationMethod,
		BkUsername:                 c.BkUsername,
		BkAppCode:                  c.BkAppCode,
		PreferStorage:              c.PreferStorage,
		BkdataDataToken:            c.BkdataDataToken,
		BkAppSecret:                c.BkAppSecret,
	}

	if sql != "" {
		params.SQL = sql
	}

	body, err := json.Marshal(params)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	size, err := c.Curl.Request(
		ctx, method,
		curl.Options{
			UrlPath: url,
			Body:    body,
			Headers: map[string]string{
				ContentType: c.ContentType,
			},
		},
		res,
	)
	if err != nil {
		return err
	}

	user := metadata.GetUser(ctx)
	metric.TsDBRequestBytes(ctx, size, user.SpaceUid, user.Source, consul.BkSqlStorageType)
	return nil
}

func (c *Client) QuerySync(ctx context.Context, sql string) *Result {
	data := &QuerySyncResultData{}
	res := c.response(data)

	url := fmt.Sprintf("%s/%s", c.Address, QuerySync)
	err := c.curl(ctx, curl.Post, url, sql, res)
	if err != nil {
		return c.failed(ctx, err)
	}

	return res
}

func (c *Client) QueryAsync(ctx context.Context, sql string) *Result {
	data := &QueryAsyncData{}
	res := c.response(data)

	url := fmt.Sprintf("%s/%s", c.Address, QueryAsync)
	err := c.curl(ctx, curl.Post, url, sql, res)
	if err != nil {
		return c.failed(ctx, err)
	}

	return res
}

func (c *Client) QueryAsyncResult(ctx context.Context, queryID string) *Result {
	data := &QueryAsyncResultData{}
	res := c.response(data)

	url := fmt.Sprintf("%s/%s/result/%s", c.Address, QueryAsync, queryID)
	err := c.curl(ctx, curl.Get, url, "", res)
	if err != nil {
		return c.failed(ctx, err)
	}

	return res
}

func (c *Client) QueryAsyncState(ctx context.Context, queryID string) *Result {
	data := &QueryAsyncStateData{}
	res := c.response(data)

	url := fmt.Sprintf("%s/%s/state/%s", c.Address, QueryAsync, queryID)
	err := c.curl(ctx, curl.Get, url, "", res)
	if err != nil {
		return c.failed(ctx, err)
	}
	return res
}

func (c *Client) response(data interface{}) *Result {
	return &Result{Data: data}
}

func (c *Client) failed(ctx context.Context, err error) *Result {
	c.Log.Errorf(ctx, err.Error())
	return &Result{
		Result:  false,
		Message: err.Error(),
		Code:    StatusFailed,
	}
}
