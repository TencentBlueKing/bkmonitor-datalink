// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tasks

import (
	"errors"
	"time"
)

// TimestampHandler 默认为ms，其他情况自行计算
type TimestampHandler func(nowTSMilli, timestamp int64, offsetTime time.Duration) int64

func getTimestampHandler(t time.Duration) TimestampHandler {
	return func(nowTSMilli int64, timestamp int64, offsetTime time.Duration) int64 {
		nanoTs := getTimestampNano(nowTSMilli*int64(time.Millisecond), timestamp, offsetTime)
		return nanoTs / int64(t)
	}
}

// 上报的时间戳单位
var timestampUnitMap = map[string]TimestampHandler{
	"ns": getTimestampHandler(time.Nanosecond),
	"ms": getTimestampHandler(time.Millisecond),
	"s":  getTimestampHandler(time.Second),
}

// GetTimestampHandler 获取时间戳处理工具
func GetTimestampHandler(timestampUnit string) (TimestampHandler, error) {
	if handler, ok := timestampUnitMap[timestampUnit]; ok {
		return handler, nil
	}
	return nil, errors.New("unknown timestamp unit")
}

func getTimestampNano(nowTSNano, timestamp int64, offsetTime time.Duration) int64 {
	// 处理用户上报采集时间，全部转化为ns级
	if timestamp < 1e6 { // epoch_minute
		timestamp = timestamp * int64(time.Minute)
	} else if timestamp < 1e12 { // epoch_second,
		timestamp = timestamp * int64(time.Second)
	} else if timestamp < 1e15 { // epoch_millisecond
		timestamp = timestamp * int64(time.Millisecond)
	} else if timestamp < 1e18 { // epoch_microsecond
		timestamp = timestamp * int64(time.Microsecond)
	} else { // epoch_nanosecond
		timestamp = timestamp * int64(time.Nanosecond)
	}
	// 计算上报数据时间与当前时间的时间差
	offset := time.Since(time.Unix(0, timestamp))

	// 如果上报时间在过去且时间偏差超过两年，使用当前时间
	// 当上报时间在未来，保留原本时间
	if timestamp == 0 || offset > offsetTime {
		timestamp = nowTSNano
	}
	// 返回的时间戳为纳秒级
	return timestamp
}
