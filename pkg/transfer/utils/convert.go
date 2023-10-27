// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/cstockton/go-conv"
)

// StringToFilePerm :
func StringToFilePerm(permStr string) (os.FileMode, error) {
	perm, err := strconv.ParseUint(permStr, 8, 32)
	if err != nil {
		return 0, err
	}
	return os.FileMode(perm), nil
}

// ParseNormalFloat64 :
func ParseNormalFloat64(value interface{}) (float64, error) {
	number, err := conv.DefaultConv.Float64(value)
	if err != nil {
		return 0, err
	} else if math.IsNaN(number) {
		return 0, fmt.Errorf("nan")
	} else if math.IsInf(number, 1) {
		return 0, fmt.Errorf("+inf")
	} else if math.IsInf(number, -1) {
		return 0, fmt.Errorf("-inf")
	}
	return number, nil
}

// ParseTimeStamp
func ParseTimeStamp(ts int64) time.Time {
	var value time.Time
	if ts < 1000000000 { // epoch_minute
		value = time.Unix(ts*60, 0)
	} else if ts < 1000000000000 { // epoch_second
		value = time.Unix(ts, 0)
	} else if ts < 1000000000000000 { // epoch_millisecond
		value = time.Unix(0, ts*int64(time.Millisecond))
	} else if ts < 1000000000000000000 { // epoch_microsecond
		value = time.Unix(0, ts*int64(time.Microsecond))
	} else { // epoch_nanosecond
		value = time.Unix(0, ts*int64(time.Nanosecond))
	}
	return value
}

func RecognizeTimeStampPrecision(ts int64) (duration time.Duration) {
	if ts < 1000000000 { // epoch_minute
		return time.Minute
	} else if ts < 1000000000000 { // epoch_second
		return time.Second
	} else if ts < 1000000000000000 { // epoch_millisecond
		return time.Millisecond
	} else if ts < 1000000000000000000 { // epoch_microsecond
		return time.Microsecond
	} else { // epoch_nanosecond
		return time.Nanosecond
	}
}

func ConvertTimeUnitAs(ts int64, unit string) int64 {
	t := ParseTimeStamp(ts)
	switch unit {
	case "s":
		return t.Unix()
	case "ms":
		return t.UnixMilli()
	case "μs":
		return t.UnixMicro()
	case "ns":
		return t.UnixNano()
	}

	return ts
}

// ParseTime
func ParseTime(v interface{}) (time.Time, error) {
	value, err := conv.DefaultConv.Time(v)
	if err == nil {
		return value, nil
	}

	ts, err := conv.DefaultConv.Int64(v)
	if err == nil {
		return ParseTimeStamp(ts), nil
	}

	str, err := conv.DefaultConv.String(v)
	if err == nil {
		return conv.DefaultConv.Time(str)
	}
	return value, err
}

// ParseFixedTimeZone
func ParseFixedTimeZone(zone int) *time.Location {
	loc := time.FixedZone(fmt.Sprintf("UTC%d", zone), zone*60*60)
	return loc
}
