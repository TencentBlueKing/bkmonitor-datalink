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
	"strings"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/spf13/cast"
)

const (
	Second      = "second"
	Millisecond = "millisecond"
	Microsecond = "microsecond"
	Nanosecond  = "nanosecond"
)

func StringToNanoUnix(s string) int64 {
	if t, ok := StringToTime(s); ok {
		return t.UnixNano()
	}

	return 0
}

func StringToTime(s string) (t time.Time, ok bool) {
	n := cast.ToInt64(s)
	if n > 1e18 {
		t = time.Unix(0, n)
	} else if n > 1e15 {
		t = time.UnixMicro(n)
	} else if n > 1e12 {
		t = time.UnixMilli(n)
	} else if n > 1e8 {
		t = time.Unix(n, 0)
	}

	if !t.IsZero() {
		ok = true
		return t, ok
	}

	timeFormat := []string{
		"2006-01-02T15:04:05.000000000Z",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05,000",
		"2006-01-02 15:04:05.000",
		"2006-01-02 15:04:05.000000",
		"2006-01-02 15:04:05.000000000",
		"2006-01-02+15:04:05",
		"01/02/2006 15:04:05",
		"2006-01-02",
		"20060102",
		"20060102150405",
		"20060102 150405",
		"20060102 150405.000",
		"20060102 150405.000000",
		"2006/01/02 15:04:05",
		"02/Jan/2006:15:04:05",
		"02/Jan/2006:15:04:05-0700",
		"02/Jan/2006:15:04:05 -0700",
		"02/Jan/2006:15:04:05-07:00",
		"02/Jan/2006:15:04:05 -07:00",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05.000",
		"20060102T150405.000-0700",
		"20060102T150405-0700",
		"20060102T150405.000000-0700",
		"2006-01-02T15:04:05.000-07:00",
		"2006-01-02T15:04:05-07:00",
		"2006-01-02T15:04:05.000000-07:00",
	}

	var err error
	for _, tf := range timeFormat {
		t, err = time.Parse(tf, s)
		if err == nil {
			// 命中规则提前退出
			ok = true
			return t, ok
		}
	}

	return t, ok
}

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
		return f, t, err
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

	return f, t, err
}

func IntPoint(d int) *int {
	return &d
}

// QueryTimestamp 将开始时间和结束时间的时间戳从 string 转换为 time.Time，根据长度判定单位
func QueryTimestamp(startTime, endTime string) (format string, start time.Time, end time.Time, err error) {
	var (
		startUnit string
		endUnit   string
	)

	// 兼容 instant 模式下，只有结束时间的情况
	if startTime == "" && endTime != "" {
		startTime = endTime
	}

	if startTime != "" {
		startUnit, start, err = ParseTimestamp(startTime)
		if err != nil {
			err = fmt.Errorf("invalid start time: %v", err)
			return format, start, end, err
		}
	} else {
		// 默认查询1小时内的数据
		start = time.Now().Add(-time.Hour * 1)
		startUnit = Second
	}

	if endTime != "" {
		endUnit, end, err = ParseTimestamp(endTime)
		if err != nil {
			err = fmt.Errorf("invalid end time: %v", err)
			return format, start, end, err
		}
	} else {
		// 默认查询1小时内的数据
		end = time.Now()
		endUnit = Second
	}

	if startUnit != endUnit {
		err = fmt.Errorf("start time and end time must have the same format")
		return format, start, end, err
	}
	format = startUnit

	return format, start, end, err
}

// MsIntMergeNs 将毫秒时间和纳秒时间戳合并为新的时间
func MsIntMergeNs(ms int64, ns time.Time) time.Time {
	return time.Unix(0, (ms-ns.UnixMilli())*1e6+ns.UnixNano())
}

// IsAlignTime 判断该聚合是否需要进行对齐
// 如果是按天聚合，则增加时区偏移量（修改该逻辑为只要有聚合就进行偏移量处理）
func IsAlignTime(t time.Duration) bool {
	if t == 0 {
		return false
	}

	if t.Seconds() > 0 {
		return true
	}

	// 只有按天的聚合才需要对齐时间
	day := 24 * time.Hour
	return t.Milliseconds()%day.Milliseconds() == 0
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

// GetRealMetricName 获取真实指标名
func GetRealMetricName(datasource, tableID string, metricNames ...string) []string {
	var metrics []string
	for _, metricName := range metricNames {
		var m []string
		// 如果是单指标查询，不用拼接 datasource
		if datasource != "" {
			m = append(m, datasource)
		}
		if tableID != "" {
			m = append(m, strings.Split(tableID, ".")...)
		}
		if metricName != "" {
			m = append(m, metricName)
		}
		metrics = append(metrics, strings.Join(m, ":"))
	}
	return metrics
}
