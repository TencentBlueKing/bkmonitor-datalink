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
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/prometheus/prometheus/model/labels"
)

const (
	Second      = "second"
	Millisecond = "millisecond"
	Microsecond = "microsecond"
	Nanosecond  = "nanosecond"
)

func MatcherToMetricName(matchers ...*labels.Matcher) string {
	for _, m := range matchers {
		if m.Name == labels.MetricName {
			if m.Type == labels.MatchEqual || m.Type == labels.MatchRegexp {
				return m.Value
			}
		}
	}

	return ""
}

func RangeDateWithUnit(unit string, start, end time.Time, step int) (dates []string) {
	var (
		addYear  int
		addMonth int
		addDay   int
		toDate   func(t time.Time) time.Time
		format   string
	)

	switch unit {
	case "year":
		addYear = step
		format = "2006"
		toDate = func(t time.Time) time.Time {
			return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())
		}
	case "month":
		addMonth = step
		format = "200601"
		toDate = func(t time.Time) time.Time {
			return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
		}
	default:
		addDay = step
		format = "20060102"
		toDate = func(t time.Time) time.Time {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
		}
	}

	for d := toDate(start); !d.After(toDate(end)); d = d.AddDate(addYear, addMonth, addDay) {
		dates = append(dates, d.Format(format))
	}

	return dates
}

// ParseTimestamp 将字符串根据格式转换为时间戳
func ParseTimestamp(s string) (f string, t time.Time, err error) {
	// 将字符串转换为int64
	val, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return
	}

	// 根据字符串长度判断单位
	switch len(s) {
	case 10: // 秒（10位）
		t = time.Unix(val, 0)
		f = Second
	case 13: // 毫秒（13位）
		sec := val / 1000
		nsec := (val % 1000) * 1e6
		t = time.Unix(sec, nsec)
		f = Millisecond
	case 16: // 微秒（16位）
		sec := val / 1e6
		nsec := (val % 1e6) * 1e3
		t = time.Unix(sec, nsec)
		f = Microsecond
	case 19: // 纳秒（19位）
		t = time.Unix(0, val)
		f = Nanosecond
	default:
		err = fmt.Errorf("unsupported timestamp length: %d", len(s))
	}

	return
}

func IntPoint(d int) *int {
	return &d
}

// QueryTimestamp 将开始时间和结束时间的时间戳从 string 转换为 time.Time，根据长度判定单位
func QueryTimestamp(s, e string) (format string, start time.Time, end time.Time, err error) {
	var (
		startUnit string
		endUnit   string
	)

	if s != "" {
		startUnit, start, err = ParseTimestamp(s)
		if err != nil {
			err = fmt.Errorf("invalid start time: %v", err)
			return
		}
	} else {
		// 默认查询1小时内的数据
		start = time.Now().Add(-time.Hour * 1)
		startUnit = Second
	}

	if e != "" {
		endUnit, end, err = ParseTimestamp(e)
		if err != nil {
			err = fmt.Errorf("invalid end time: %v", err)
			return
		}
	} else {
		// 默认查询1小时内的数据
		end = time.Now()
		endUnit = Second
	}

	if startUnit != endUnit {
		err = fmt.Errorf("start time and end time must have the same format")
		return
	}
	format = startUnit

	return
}

// MsIntMergeNs 将毫秒时间和纳秒时间戳合并为新的时间
func MsIntMergeNs(ms int64, ns time.Time) time.Time {
	return time.Unix(0, (ms-ns.UnixMilli())*1e6+ns.UnixNano())
}

// TimeOffset 根据 timezone 偏移对齐
func TimeOffset(t time.Time, timezone string, step time.Duration) (string, time.Time) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	t0 := t.In(loc)
	_, offset := t0.Zone()
	outTimezone := t0.Location().String()
	offsetDuration := time.Duration(offset) * time.Second
	t1 := t.Add(offsetDuration)
	t2 := time.Unix(int64(math.Floor(float64(t1.Unix())/step.Seconds())*step.Seconds()), 0)
	t3 := t2.Add(offsetDuration * -1).In(loc)
	return outTimezone, t3
}
