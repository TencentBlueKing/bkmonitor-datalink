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

	"github.com/uptrace/opentelemetry-go-extra/otelzap"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

func NewMockCurl(data map[string]string, log *otelzap.Logger) *TestCurl {
	return &TestCurl{
		log:  log,
		data: data,
	}
}

type TestCurl struct {
	log  *otelzap.Logger
	data map[string]string

	Url    string
	Params []byte
}

func (c *TestCurl) resp(body string) *http.Response {
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    200,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Body:          io.NopCloser(bytes.NewBufferString(body)),
		ContentLength: int64(len(body)),
		Header:        make(http.Header, 0),
	}
}

func (c *TestCurl) Request(ctx context.Context, method string, opt Options) (*http.Response, error) {
	log.Infof(ctx, "http %s: %s", method, opt.UrlPath)

	c.Url = opt.UrlPath
	c.Params = opt.Body

	if res, ok := c.data[opt.UrlPath]; ok {
		return c.resp(res), nil
	} else {
		return nil, fmt.Errorf("mock data is not exists: " + opt.UrlPath)
	}
}
