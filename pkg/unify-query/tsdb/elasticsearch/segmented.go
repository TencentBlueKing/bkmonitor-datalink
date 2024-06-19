// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/segmented"
)

type querySegmentOption struct {
	start    int64
	end      int64
	interval int64

	docCount  int64
	storeSize int64
}

func newRangeSegment(opt *querySegmentOption) ([][2]int64, error) {
	s := segmented.NewSegmented()
	// 根据文本数量计算分片数
	maxDocCount := viper.GetInt64(SegmentDocCountPath)
	docCountSegmentNum := intMathCeil(opt.docCount, maxDocCount)
	// 根据存储大小计算分片数
	maxStoreSizeString := viper.GetString(SegmentStoreSizePath)
	maxStoreSize, err := parseSizeString(maxStoreSizeString)
	if err != nil {
		return nil, err
	}
	storeSizeSegmentNum := intMathCeil(opt.storeSize, maxStoreSize)

	// 对比文本数两和存储大小计算出来的分片数，取更大的那个
	var segmentNum int64
	if docCountSegmentNum > storeSizeSegmentNum {
		segmentNum = docCountSegmentNum
	} else {
		segmentNum = storeSizeSegmentNum
	}

	// 最大分片数
	maxSegmentNum := viper.GetInt64(SegmentMaxNumPath)
	if segmentNum > maxSegmentNum {
		segmentNum = maxSegmentNum
	}

	// 最小按小时分片
	maxTimeRange := viper.GetDuration(SegmentMaxTimeRangePath)
	left := opt.end - opt.start
	timeMaxSegNum := intMathCeil(left, maxTimeRange.Milliseconds())
	if segmentNum > timeMaxSegNum {
		segmentNum = timeMaxSegNum
	}

	// 如果分片数为 1 则无需分片
	if segmentNum == 1 {
		return [][2]int64{{opt.start, opt.end}}, nil
	}

	seg := intMathCeil(left, segmentNum)

	// 根据聚合周期 interval 对齐分片数，因为进行了聚合操作，所以分片周期不能小于聚合周期，不然计算出来的时间不对
	if opt.interval > 0 {
		intervalNum := intMathCeil(left, opt.interval)
		seg = intMathCeil(intervalNum, segmentNum) * opt.interval
	}

	// 对齐时间戳
	data := opt.start
	for {
		// 判断最后一次是否超出一个 step 的距离
		if (opt.end - opt.interval) <= data {
			s.Add(opt.end)
			break
		}
		s.Add(data)
		data += seg
	}

	list := make([][2]int64, 0, s.Count())
	for _, l := range s.List() {
		list = append(list, [2]int64{l.Min, l.Max})
	}

	return list, nil
}
