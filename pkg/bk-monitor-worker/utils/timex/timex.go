// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package timex

import (
	"time"
)

// UnixTime2Time unix time to time
func UnixTime2Time(t int64) time.Time {
	if t == 0 {
		return time.Time{}
	}
	return time.Unix(t, 0)
}

// Clock interface
type Clock interface {
	Now() time.Time
}

type TimeClock struct{}

func NewTimeClock() Clock {
	return &TimeClock{}
}

// Now 当前时间
func (c *TimeClock) Now() time.Time {
	return time.Now()
}

const (
	TimeLayout = "2006-01-02 15:04:05"
)

// StringToTime transform string to time
func StringToTime(timeStr string) (time.Time, error) {
	_time, err := time.ParseInLocation(TimeLayout, timeStr, time.Local)
	if err != nil {
		return time.Time{}, err
	}
	return _time, nil
}
