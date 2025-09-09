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
	"io"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const (
	Get  = "GET"
	Post = "POST"
)

var bufPool = sync.Pool{
	New: func() any {
		return bytes.NewBuffer(make([]byte, 0, 1024))
	},
}

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
	WithDecoder(decoder func(ctx context.Context, reader io.Reader, resp any) (int, error))
	Request(ctx context.Context, method string, opt Options, res any) (int, error)
}

// HttpCurl http 请求方法
type HttpCurl struct {
	Log     *log.Logger
	decoder func(ctx context.Context, reader io.Reader, res any) (int, error)
}

func (c *HttpCurl) WithDecoder(decoder func(ctx context.Context, reader io.Reader, res any) (int, error)) {
	c.decoder = decoder
}

func (c *HttpCurl) Request(ctx context.Context, method string, opt Options, res any) (size int, err error) {
	ctx, span := trace.NewSpan(ctx, "http-curl")
	defer span.End(&err)

	client := http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		Timeout:   opt.Timeout,
	}

	if opt.UrlPath == "" {
		err = fmt.Errorf("url is emtpy")
		return size, err
	}

	req, err := http.NewRequestWithContext(ctx, method, opt.UrlPath, bytes.NewBuffer(opt.Body))
	if err != nil {
		c.Log.Errorf(ctx, "client new request error:%v", err)
		return size, err
	}

	if opt.UserName != "" {
		req.SetBasicAuth(opt.UserName, opt.Password)
	}

	span.Set("req-http-method", method)
	span.Set("req-http-path", opt.UrlPath)

	c.Log.Infof(ctx, "curl request: %s[%s] body:%s", method, opt.UrlPath, opt.Body)

	for k, v := range opt.Headers {
		if k != "" && v != "" {
			req.Header.Set(k, v)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return size, err
	}

	buf := bufPool.Get().(*bytes.Buffer)
	defer func() {
		_ = resp.Body.Close()
		buf.Reset()
		bufPool.Put(buf)
	}()

	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("http code error: %s", resp.Status)
		return size, err
	}

	if c.decoder != nil {
		size, err = c.decoder(ctx, resp.Body, res)
		return size, err
	} else {
		_, err = io.Copy(buf, resp.Body)
		if err != nil {
			return size, err
		}
		size = buf.Len()

		decoder := json.NewDecoder(buf)
		err = decoder.Decode(&res)
		return size, err
	}
}
