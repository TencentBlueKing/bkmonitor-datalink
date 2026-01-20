// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mock

import (
	"io"
	"net/http"
	"sync"

	"github.com/jarcoal/httpmock"
)

// SurrealDB Mock 地址和默认配置
const (
	SurrealDBUrl     = "http://127.0.0.1:8000"
	DefaultNamespace = "default"
	DefaultDatabase  = "test"
)

// SurrealDB mock 数据存储（类似 ES 的 resultData）
var SurrealDB = &surrealDBMockData{}

type surrealDBMockData struct {
	lock sync.RWMutex
	data map[string]any
}

// Set 设置 mock 数据（精确匹配）
// key: SQL 查询语句
// value: JSON 格式的响应字符串
func (s *surrealDBMockData) Set(in map[string]any) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if s.data == nil {
		s.data = make(map[string]any)
	}
	for k, v := range in {
		s.data[k] = v
	}
}

// Clear 清空所有 mock 数据
func (s *surrealDBMockData) Clear() {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.data = make(map[string]any)
}

// Get 获取 mock 数据（精确匹配）
func (s *surrealDBMockData) Get(query string) (any, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	d, ok := s.data[query]
	return d, ok
}

// RegisterHandler 注册 SurrealDB 的 httpmock 处理器
func RegisterHandler() {
	httpmock.RegisterResponder(http.MethodPost, SurrealDBUrl+"/sql",
		func(r *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(r.Body)
			query := string(body)

			// 尝试从 mock 数据中获取响应（精确匹配）
			if resp, ok := SurrealDB.Get(query); ok {
				switch v := resp.(type) {
				case string:
					return httpmock.NewStringResponse(http.StatusOK, v), nil
				default:
					return httpmock.NewJsonResponse(http.StatusOK, v)
				}
			}

			// 默认返回空结果
			return httpmock.NewStringResponse(http.StatusOK, EmptyResponse), nil
		})
}
