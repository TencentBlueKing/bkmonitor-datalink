// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package instance

import (
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
)

type Request struct {
	Namespace string              `json:"namespace"`
	SecretId  string              `json:"secretId"`
	SecretKey string              `json:"secretKey"`
	Region    string              `json:"region"`
	Filters   map[string][]string `json:"filters"`
}

func pickEndpoint(ep string) string {
	if !configs.G().QCloudMonitor.Private {
		return ep
	}

	parts := strings.Split(ep, ".")

	var dst []string
	dst = append(dst, parts[0])
	dst = append(dst, "internal")
	dst = append(dst, parts[1:]...)
	return strings.Join(dst, ".")
}

type Querier interface {
	Query(r *Request) ([]any, error)
}

var queriers = make(map[string]Querier)

func Register(namespace string, q Querier) {
	if _, ok := queriers[namespace]; ok {
		panic("duplicate register querier")
	}
	queriers[namespace] = q
}

func Get(namespace string) (Querier, bool) {
	q, ok := queriers[namespace]
	return q, ok
}
