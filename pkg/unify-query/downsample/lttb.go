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
	"math"

	"github.com/prometheus/prometheus/promql"
)

// LTTB 降采样算法实现
// 算法论文 https://skemman.is/bitstream/1946/15343/3/SS_MSthesis.pdf
// 实现参考 https://github.com/dgryski/go-lttb
func lttbFunc(values []promql.Point, threshold int) []promql.Point {
	var out []promql.Point
	// 小于2个点的降采样无意义 Drop this Vector element.
	if len(values) < 2 {
		return values
	}

	if threshold >= len(values) || threshold <= 2 {
		return values // Nothing to do
	}

	// Bucket size. Leave room for start and end data points
	every := float64(len(values)-2) / float64(threshold-2)

	// Always add the first point
	out = append(out, values[0])

	bucketStart := 1
	bucketCenter := int(math.Floor(every)) + 1

	var a int

	for i := 0; i < threshold-2; i++ {

		bucketEnd := int(math.Floor(float64(i+2)*every)) + 1

		// Calculate point average for next bucket (containing c)
		avgRangeStart := bucketCenter
		avgRangeEnd := bucketEnd

		if avgRangeEnd >= len(values) {
			avgRangeEnd = len(values)
		}

		avgRangeLength := float64(avgRangeEnd - avgRangeStart)

		var avgX, avgY float64
		for ; avgRangeStart < avgRangeEnd; avgRangeStart++ {
			avgX += float64(values[avgRangeStart].T)
			avgY += values[avgRangeStart].V
		}
		avgX /= avgRangeLength
		avgY /= avgRangeLength

		// Get the range for this bucket
		rangeOffs := bucketStart
		rangeTo := bucketCenter

		// Point a
		pointAX := float64(values[a].T)
		pointAY := values[a].V

		maxArea := -1.0

		var nextA int
		for ; rangeOffs < rangeTo; rangeOffs++ {
			// Calculate triangle area over three buckets
			area := (pointAX-avgX)*(values[rangeOffs].V-pointAY) - (pointAX-float64(values[rangeOffs].T))*(avgY-pointAY)
			// We only care about the relative area here.
			// Calling math.Abs() is slower than squaring
			area *= area
			if area > maxArea {
				maxArea = area
				nextA = rangeOffs // Next a is this b
			}
		}

		// Pick this point from the bucket
		out = append(out, values[nextA])

		a = nextA // This a is the next a (chosen b)

		bucketStart = bucketCenter
		bucketCenter = bucketEnd
	}

	// Always add last
	out = append(out, values[len(values)-1])

	return out
}
