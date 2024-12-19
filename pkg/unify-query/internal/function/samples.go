// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package function

import (
	"sort"

	"github.com/prometheus/prometheus/prompb"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
)

// MergeSamplesWithSumAndSort 合并 samples 数据，如果相同时间的则相加，并且按照时间排序
func MergeSamplesWithSumAndSort(samplesList ...[]prompb.Sample) []prompb.Sample {
	// 生成 sampleMap 用户合并计算
	sampleMap := make(map[int64]float64)
	// 生成时间 set 用于排序
	timestampSet := set.New[int64]()

	for _, samples := range samplesList {
		for _, sample := range samples {
			timestampSet.Add(sample.GetTimestamp())
			sampleMap[sample.GetTimestamp()] += sample.GetValue()
		}
	}

	out := make([]prompb.Sample, timestampSet.Size())

	// 正序
	timestamps := timestampSet.ToArray()
	sort.Slice(timestamps, func(i, j int) bool {
		return timestamps[i] < timestamps[j]
	})

	for i, timestamp := range timestamps {
		out[i] = prompb.Sample{
			Timestamp: timestamp,
			Value:     sampleMap[timestamp],
		}
	}
	return out
}

// MergeSamplesWithUnionAndSort 合并 samples 数据，如果相同时间的则追加，并且按照时间排序
func MergeSamplesWithUnionAndSort(samplesList ...[]prompb.Sample) []prompb.Sample {
	var out []prompb.Sample
	for _, samples := range samplesList {
		out = append(out, samples...)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].GetTimestamp() < out[j].GetTimestamp()
	})
	return out
}
