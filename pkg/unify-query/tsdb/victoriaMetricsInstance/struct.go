// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package victoriaMetricsInstance

type Metric map[string]string

type Series struct {
	Metric Metric          `json:"metric"`
	Value  []interface{}   `json:"value,omitempty"`
	Values [][]interface{} `json:"values,omitempty"`
}

// Data 查询返回结构体
type Data struct {
	Status string `json:"status"`

	ErrorType string `json:"errorType,omitempty"`
	Error     string `json:"error,omitempty"`

	IsPartial bool `json:"isPartial,omitempty"`
	Data      struct {
		ResultType string   `json:"resultType"`
		Result     []Series `json:"result"`
	} `json:"data,omitempty"`
}
