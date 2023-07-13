// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"fmt"
	"net/http"
)

// RouteInfo 路由信息
type RouteInfo struct {
	// Source 路由注册来源
	Source string `json:"source"`

	// HttpMethod 路由注册方法
	HttpMethod string `json:"http_method"`

	// Path 路由注册路径
	Path string `json:"path"`
}

func (r RouteInfo) Key() string {
	return fmt.Sprintf("%s %s", r.HttpMethod, r.Path)
}

func (r RouteInfo) ID() string {
	return fmt.Sprintf("%s/%s/%s", r.Source, r.HttpMethod, r.Path)
}

type StatusCode int

func (s StatusCode) S() string {
	return fmt.Sprintf("%d", s)
}

const (
	StatusCodeOK              StatusCode = http.StatusOK
	StatusBadRequest          StatusCode = http.StatusBadRequest
	StatusCodeUnauthorized    StatusCode = http.StatusUnauthorized
	StatusCodeTooManyRequests StatusCode = http.StatusTooManyRequests
)

func KB(n int) float64 {
	return float64(n) * 1024
}

func MB(n int) float64 {
	return float64(n) * 1024 * 1024
}

var (
	// DefSizeDistribution 默认的数据量桶分布
	DefSizeDistribution = []float64{
		KB(10), KB(100), KB(250), KB(500), MB(1), MB(5), MB(8),
		MB(10), MB(20), MB(30), MB(50), MB(80), MB(100), MB(150), MB(200),
	}

	// DefObserveDuration 默认的时间桶分布
	DefObserveDuration = []float64{
		0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300, 600,
	}
)
