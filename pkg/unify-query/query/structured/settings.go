// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

const (
	MIN   = "min"
	MAX   = "max"
	SUM   = "sum"
	COUNT = "count"
	LAST  = "last"
	MEAN  = "mean"
	AVG   = "avg"

	MinOT   = "min_over_time"
	MaxOT   = "max_over_time"
	SumOT   = "sum_over_time"
	CountOT = "count_over_time"
	LastOT  = "last_over_time"
	AvgOT   = "avg_over_time"
)

var domSampledFunc = map[string]string{
	MIN + MinOT:   MIN,
	MAX + MaxOT:   MAX,
	SUM + SumOT:   SUM,
	AVG + AvgOT:   AVG,
	MEAN + AvgOT:  AVG,
	SUM + CountOT: COUNT,
}
