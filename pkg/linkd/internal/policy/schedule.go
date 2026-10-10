// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // 容器镜像没有系统 zoneinfo 时仍保持策略时区语义。
)

// ActiveTime 保留 KAC 生效时间配置；周期时段不能跨午夜，所有端点按秒包含。
type ActiveTime struct {
	Period      string `json:"period"`
	OpenOnce    string `json:"open_datetime_once,omitempty"`
	CloseOnce   string `json:"close_datetime_once,omitempty"`
	OpenClock   string `json:"open_clock_time,omitempty"`
	CloseClock  string `json:"close_clock_time,omitempty"`
	DaysOfWeek  string `json:"day_for_week,omitempty"`
	DaysOfMonth string `json:"day_for_month,omitempty"`
}

type activeInterval struct {
	period      string
	start, end  time.Time
	open, close int
	days        map[int]bool
}

// Schedule 是只读时间表，可跨 goroutine 共享；空规则集不生效。
type Schedule struct {
	zone      *time.Location
	intervals []activeInterval
}

// CompileSchedule 固定时区并拒绝无效日期、跨午夜及含歧义的本地绝对时间。
func CompileSchedule(zone string, rules []ActiveTime) (*Schedule, error) {
	if zone == "" {
		zone = "Asia/Shanghai"
	}
	if len(zone) > 128 || len(rules) > 64 {
		return nil, fmt.Errorf("schedule exceeds timezone or interval budget")
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return nil, fmt.Errorf("timezone is unknown")
	}
	schedule := &Schedule{zone: location, intervals: make([]activeInterval, 0, len(rules))}
	for i, rule := range rules {
		interval, err := compileInterval(location, rule)
		if err != nil {
			return nil, fmt.Errorf("activate_times[%d]: %w", i, err)
		}
		schedule.intervals = append(schedule.intervals, interval)
	}
	return schedule, nil
}

func compileInterval(zone *time.Location, rule ActiveTime) (activeInterval, error) {
	result := activeInterval{period: rule.Period}
	var err error
	// KAC 持久化所有周期的字段；只读取当前周期的字段，忽略其他周期的占位或旧值。
	if rule.Period == "once" {
		result.start, err = parsePolicyTime(zone, rule.OpenOnce)
		if err != nil {
			return result, err
		}
		result.end, err = parsePolicyTime(zone, rule.CloseOnce)
		if err != nil {
			return result, err
		}
		if result.end.Before(result.start) {
			return result, fmt.Errorf("once close precedes open")
		}
		return result, nil
	}
	result.open, err = parseClock(rule.OpenClock)
	if err != nil {
		return result, err
	}
	result.close, err = parseClock(rule.CloseClock)
	if err != nil {
		return result, err
	}
	if result.close < result.open {
		return result, fmt.Errorf("cross-midnight schedule must be split")
	}
	switch rule.Period {
	case "everyday":
	case "every_week":
		result.days, err = parseDays(rule.DaysOfWeek, 7)
	case "every_month":
		result.days, err = parseDays(rule.DaysOfMonth, 31)
	default:
		return result, fmt.Errorf("unsupported period")
	}
	return result, err
}

func parseClock(value string) (int, error) {
	parsed, err := time.Parse("15:04:05", value)
	if err != nil || parsed.Format("15:04:05") != value {
		return 0, fmt.Errorf("clock must be HH:MM:SS")
	}
	return parsed.Hour()*3600 + parsed.Minute()*60 + parsed.Second(), nil
}

func parseDays(value string, limit int) (map[int]bool, error) {
	days := map[int]bool{}
	if value == "*" {
		for day := 1; day <= limit; day++ {
			days[day] = true
		}
		return days, nil
	}
	if value == "" || len(value) > 128 {
		return nil, fmt.Errorf("day selector is required and bounded")
	}
	for _, part := range strings.Split(value, ",") {
		day, err := strconv.Atoi(part)
		if err != nil || day < 1 || day > limit || strconv.Itoa(day) != part || days[day] {
			return nil, fmt.Errorf("invalid or duplicate day selector")
		}
		days[day] = true
	}
	return days, nil
}

func parsePolicyTime(zone *time.Location, value string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		if parsed.Nanosecond() != 0 {
			return time.Time{}, fmt.Errorf("schedule requires whole-second endpoints")
		}
		return parsed.UTC(), nil
	}
	const layout = "2006-01-02 15:04:05"
	parsed, err := time.ParseInLocation(layout, value, zone)
	if err != nil || parsed.Format(layout) != value {
		return time.Time{}, fmt.Errorf("invalid local datetime; use RFC3339 for explicit offset")
	}
	_, offset := parsed.Zone()
	for _, other := range []time.Time{parsed.Add(-24 * time.Hour), parsed.Add(24 * time.Hour)} {
		_, candidate := other.Zone()
		if offset != candidate && parsed.Add(time.Duration(offset-candidate)*time.Second).In(zone).Format(layout) == value {
			return time.Time{}, fmt.Errorf("ambiguous local datetime requires RFC3339 offset")
		}
	}
	return parsed.UTC(), nil
}

// Active 与 KAC 一致按整秒评估，结束秒的子秒仍有效；周一为 1，多个时段取并集。
func (s *Schedule) Active(at time.Time) bool {
	if at.IsZero() {
		return false
	}
	at = at.Truncate(time.Second)
	local := at.In(s.zone)
	clock := local.Hour()*3600 + local.Minute()*60 + local.Second()
	for _, rule := range s.intervals {
		if rule.period == "once" {
			if !at.Before(rule.start) && !at.After(rule.end) {
				return true
			}
			continue
		}
		if clock < rule.open || clock > rule.close {
			continue
		}
		switch rule.period {
		case "everyday":
			return true
		case "every_week":
			day := int(local.Weekday())
			if day == 0 {
				day = 7
			}
			if rule.days[day] {
				return true
			}
		case "every_month":
			if rule.days[local.Day()] {
				return true
			}
		}
	}
	return false
}

// Occurrence 标识配置所定义的当前时间段实例；周期时段使用本地日期、规则序号和时区偏移。
// 它区分每日/每周复现及夏令时折叠，避免复用已结束的 ShieldBinding 身份；不以调用时刻生成随机身份。
func (s *Schedule) Occurrence(at time.Time) (string, error) {
	if s == nil || !s.Active(at) {
		return "", fmt.Errorf("schedule is inactive")
	}
	parts := []string{}
	local := at.In(s.zone)
	_, offset := local.Zone()
	for i, rule := range s.intervals {
		single := &Schedule{zone: s.zone, intervals: []activeInterval{rule}}
		if !single.Active(at) {
			continue
		}
		if rule.period == "once" {
			parts = append(parts, fmt.Sprintf("once:%d", i))
		} else {
			parts = append(parts, fmt.Sprintf("%s:%d:%d", local.Format("2006-01-02"), i, offset))
		}
	}
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
