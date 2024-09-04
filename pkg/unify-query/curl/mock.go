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
	"context"
	"encoding/json"
	"io"
)

type MockCurl struct {
	f    func(opt Options) []byte
	Opts Options
}

var _ Curl = &MockCurl{}

func (c *MockCurl) WithDecoder(decoder func(ctx context.Context, reader io.Reader, resp any) (int, error)) {
	return
}

func (c *MockCurl) WithF(f func(opt Options) []byte) {
	c.f = f
}

func (c *MockCurl) Request(ctx context.Context, method string, opt Options, res interface{}) (int, error) {
	c.Opts = opt

	var out []byte
	if c.f != nil {
		out = c.f(opt)
	}

	if len(out) > 0 {
		err := json.Unmarshal(out, res)
		return len(out), err
	}
	return 0, nil
}
