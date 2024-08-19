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
	"encoding/json"
	"io"
	"net/http"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

func NewMockCurl(data map[string]string, log *log.Logger) *TestCurl {
	return &TestCurl{
		log:  log,
		data: data,
	}
}

type TestCurl struct {
	log  *log.Logger
	data map[string]string

	Url    string
	Params []byte
}

func (c *TestCurl) WithDecoder(decoder func(ctx context.Context, reader io.Reader, resp interface{}) (int, error)) {
	return
}

func (c *TestCurl) hashOption(opt Options) string {
	s := opt.UrlPath + string(opt.Body)
	return s
}

func (c *TestCurl) Request(ctx context.Context, method string, opt Options, res interface{}) (int, error) {
	c.log.Infof(ctx, "http %s: %s", method, opt.UrlPath)

	c.Url = opt.UrlPath
	c.Params = opt.Body

	var (
		err error
		out string
		ok  bool
	)

	hashKey := c.hashOption(opt)
	if out, ok = c.data[hashKey]; ok {
		err = json.Unmarshal([]byte(out), res)
	} else {
		err = errors.New("mock data is not exists: " + hashKey)
	}

	return len(out), err
}

var _ Curl = &TestCurl{}

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
