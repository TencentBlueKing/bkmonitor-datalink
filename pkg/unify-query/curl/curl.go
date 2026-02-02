// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package curl

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/uptrace/opentelemetry-go-extra/otelzap"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

var (
	// 全局共享的 Transport，避免每次请求创建新的连接池
	defaultTransport = &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		DisableKeepAlives:   false,
	}

	// 包装 OpenTelemetry 的 Transport（只创建一次）
	otelTransport = otelhttp.NewTransport(defaultTransport)
)

const (
	Get  = "GET"
	Post = "POST"
)

// Options Curl 入参
type Options struct {
	UrlPath string
	Headers map[string]string
	Body    []byte

	UserName string
	Password string
}

type Curl interface {
	Request(ctx context.Context, method string, opt Options) (*http.Response, error)
}

// HttpCurl http 请求方法
type HttpCurl struct {
	Log *otelzap.Logger
}

// Request 公共调用方法实现
func (c *HttpCurl) Request(ctx context.Context, method string, opt Options) (*http.Response, error) {
	var (
		span oleltrace.Span
	)
	ctx, span = trace.IntoContext(ctx, trace.TracerName, "http-curl")
	if span != nil {
		defer span.End()
	}

	// 复用全局 Transport，避免连接泄漏
	client := http.Client{
		Transport: otelTransport,
	}
	c.Log.Ctx(ctx).Info(fmt.Sprintf("[%s] %s", method, opt.UrlPath))

	req, err := http.NewRequestWithContext(ctx, method, opt.UrlPath, bytes.NewBuffer(opt.Body))
	if err != nil {
		c.Log.Ctx(ctx).Error(fmt.Sprintf("client new request error:%s", err))
		return nil, err
	}

	if opt.UserName != "" {
		req.SetBasicAuth(opt.UserName, opt.Password)
	}

	trace.InsertStringIntoSpan("req-http-method", method, span)
	trace.InsertStringIntoSpan("req-http-path", opt.UrlPath, span)
	trace.InsertStringIntoSpan("req-http-headers", fmt.Sprintf("%+v", opt.Headers), span)
	trace.InsertStringIntoSpan("req-http-body", string(opt.Body), span)

	key := fmt.Sprintf("%s%s", opt.UrlPath, opt.Body)
	for k, v := range opt.Headers {
		key = fmt.Sprintf("%s%s%s", key, k, v)
		req.Header.Set(k, v)
	}

	return client.Do(req)
}
