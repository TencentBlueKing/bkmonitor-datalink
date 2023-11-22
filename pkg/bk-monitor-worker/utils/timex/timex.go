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
	"fmt"
	"regexp"
	"strconv"
	"strings"
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

// ParsePyDateFormat 解析python日期格式化字符串
func ParsePyDateFormat(dataFormat string) string {
	dataFormat = strings.ReplaceAll(dataFormat, "%Y", "2006")
	dataFormat = strings.ReplaceAll(dataFormat, "%y", "06")
	dataFormat = strings.ReplaceAll(dataFormat, "%m", "01")
	dataFormat = strings.ReplaceAll(dataFormat, "%d", "02")
	dataFormat = strings.ReplaceAll(dataFormat, "%H", "15")
	dataFormat = strings.ReplaceAll(dataFormat, "%M", "04")
	dataFormat = strings.ReplaceAll(dataFormat, "%s", "05")
	return dataFormat
}

func TimeStrToTime(timeStr, format string, timeZone int8) *time.Time {
	utcTime, err := time.Parse(format, timeStr)
	if err != nil {
		return nil
	}
	realTime := utcTime.Add(time.Duration(timeZone) * time.Hour)
	return &realTime
}

// ParseDuration 扩展time.ParseDuration支持单位天d和周w
func ParseDuration(s string) (time.Duration, error) {
	// 使用正则表达式提取数字和单位
	re := regexp.MustCompile(`(\d+)([a-zA-Z]+)`)
	matchesList := re.FindAllStringSubmatch(s, -1)
	var valueSum time.Duration
	for _, matches := range matchesList {
		if len(matches) != 3 {
			return 0, fmt.Errorf("invalid input format")
		}
		// 提取数字和单位
		value, err := strconv.Atoi(matches[1])
		if err != nil {
			return 0, err
		}
		unit := matches[2]
		// 将非标准单位转换为标准单位
		switch unit {
		case "d":
			valueSum += time.Duration(value) * 24 * time.Hour
		case "w":
			valueSum += time.Duration(value) * 7 * 24 * time.Hour
		default:
			value, err := time.ParseDuration(matches[0])
			if err != nil {
				return 0, err
			}
			valueSum += value
		}
	}
	return valueSum, nil
}
