// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	MethodGet  = "GET"
	MethodPost = "POST"
)

// Options HTTP 请求配置
type Options struct {
	BaseUrl string
	Params  url.Values
	Body    []byte
	Headers map[string]string

	UserName string
	Password string
}

type Client interface {
	Request(ctx context.Context, method string, opt Options) (*http.Response, error)
	Get(ctx context.Context, baseUrl string, params url.Values, baseOpt Options) (*http.Response, error)
	Post(ctx context.Context, baseUrl string, body []byte, contentType string, baseOpt Options) (*http.Response, error)
}

func NewClient() Client {
	return &NetHttpClient{
		client: http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)},
	}
}

// NetHttpClient 基于 net/http 进行封装
type NetHttpClient struct {
	client http.Client
}

// Request 公共调用方法实现
func (c *NetHttpClient) Request(ctx context.Context, method string, opt Options) (*http.Response, error) {
	var (
		span trace.Span
	)
	ctx, span = otel.Tracer("http").Start(ctx, "http-req")
	if span != nil {
		defer span.End()
	}

	// 拼接完整请求地址
	u, err := url.Parse(opt.BaseUrl)
	if err != nil {
		return nil, err
	}
	if opt.Params != nil {
		u.RawQuery = opt.Params.Encode()
	}
	fullUrl := u.String()
	logger.Info(fmt.Sprintf("[%s] %s, %s", method, fullUrl, string(opt.Body)))
	req, err := http.NewRequestWithContext(ctx, method, fullUrl, bytes.NewBuffer(opt.Body))
	if err != nil {
		logger.Error(fmt.Sprintf("client new request error: %s", err))
		return nil, err
	}

	if opt.UserName != "" {
		req.SetBasicAuth(opt.UserName, opt.Password)
	}

	span.SetAttributes(attribute.String("req-http-method", method))
	span.SetAttributes(attribute.String("req-http-path", fullUrl))
	span.SetAttributes(attribute.String("req-http-headers", fmt.Sprintf("%+v", opt.Headers)))
	span.SetAttributes(attribute.String("req-http-body", string(opt.Body)))

	for k, v := range opt.Headers {
		req.Header.Set(k, v)
	}
	return c.client.Do(req)
}

func (c *NetHttpClient) Get(ctx context.Context, baseUrl string, params url.Values, baseOpt Options) (*http.Response, error) {
	baseOpt.BaseUrl = baseUrl
	baseOpt.Params = params
	return c.Request(ctx, MethodGet, baseOpt)
}

func (c *NetHttpClient) Post(ctx context.Context, baseUrl string, body []byte, contentType string, baseOpt Options) (*http.Response, error) {
	baseOpt.BaseUrl = baseUrl
	baseOpt.Body = body
	if baseOpt.Headers == nil {
		baseOpt.Headers = make(map[string]string)
	}
	if contentType != "" {
		baseOpt.Headers["Content-Type"] = contentType
	} else {
		baseOpt.Headers["Content-Type"] = "application/json"
	}
	return c.Request(ctx, MethodPost, baseOpt)
}
