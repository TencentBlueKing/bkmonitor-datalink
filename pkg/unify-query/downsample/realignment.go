// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package downsample

import (
	"github.com/prometheus/prometheus/promql"
)

// RealignPoints 重新对齐数据点, LTTB特点, 数据非均匀, 这里强制对齐
func RealignPoints(points []promql.Point, start, end int64, interval int64) []promql.Point {
	pointCount := len(points)
	alignPoints := make([]promql.Point, 0, len(points))
	// 区间是一个左闭右开区间，所以最后一个点不需要（ts < end）
	for ts, step := start, 0; ts < end; ts += interval {
		if step >= pointCount {
			break
		}
		alignPoints = append(alignPoints, promql.Point{T: ts, V: points[step].V})
		step++
	}
	return alignPoints
}
