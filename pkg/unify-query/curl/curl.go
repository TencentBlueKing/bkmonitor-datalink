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
	"fmt"
	"net/http"
	"time"

	"github.com/uptrace/opentelemetry-go-extra/otelzap"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
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

	Timeout time.Duration
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

	client := http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		Timeout:   opt.Timeout,
	}

	if opt.UrlPath == "" {
		return nil, fmt.Errorf("url is emtpy")
	}

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
	log.Infof(ctx, "%s", opt.Body)

	for k, v := range opt.Headers {
		if k != "" && v != "" {
			req.Header.Set(k, v)
		}
	}

	return client.Do(req)
}
