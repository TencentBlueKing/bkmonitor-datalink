// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package function_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
)

func TestMsIntMergeNs(t *testing.T) {
	ms := int64(1625097612345) // 2021-07-01 00:00:12.345 UTC

	tests := []struct {
		name     string
		ms       int64
		ns       time.Time
		expected time.Time
	}{
		{
			name:     "basic merge",
			ms:       ms,
			ns:       time.Date(2021, 5, 1, 0, 0, 0, 1234567890, time.UTC),
			expected: time.Date(2021, 7, 1, 0, 0, 12, 345567890, time.UTC),
		},
		{
			name:     "nanosecond last six digits",
			ms:       ms,
			ns:       time.Date(2023, 5, 1, 0, 0, 0, 123456789, time.UTC),
			expected: time.Date(2021, 7, 1, 0, 0, 12, 345456789, time.UTC),
		},
		{
			name:     "nanosecond overflow handling",
			ms:       ms,
			ns:       time.Date(2022, 5, 1, 0, 0, 0, 1999999999, time.UTC),
			expected: time.Date(2021, 7, 1, 0, 0, 12, 345999999, time.UTC),
		},
		{
			name:     "date boundary merge",
			ms:       1640995199999, // 2021-12-31 23:59:59.999 UTC
			ns:       time.Date(2022, 1, 1, 21, 57, 57, 999999999, time.UTC),
			expected: time.Date(2021, 12, 31, 23, 59, 59, 999999999, time.UTC),
		},
		{
			name:     "different timezone conversion",
			ms:       ms,
			ns:       time.Date(2021, 5, 1, 8, 0, 0, 123456789, time.UTC),
			expected: time.Date(2021, 7, 1, 0, 0, 12, 345456789, time.UTC),
		},
		{
			name:     "zero value handling",
			ms:       0,
			ns:       time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
			expected: time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "time component preservation",
			ms:       ms,
			ns:       time.Date(2023, 5, 1, 1, 2, 3, 456789000, time.UTC),
			expected: time.Date(2021, 7, 1, 0, 0, 12, 345789000, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := function.MsIntMergeNs(tt.ms, tt.ns)
			if !got.Equal(tt.expected) {
				t.Errorf("MsIntMergeNs() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestQueryTimestamp(t *testing.T) {
	// 固定参考时间（2021-01-01 00:00:00 UTC）
	refTime := time.Unix(1609459200, 0)

	defaultEnd := time.Now()
	defaultStart := time.Now().Add(-time.Hour * 1)

	tests := []struct {
		name        string
		s           string
		e           string
		wantFormat  string
		wantStart   time.Time
		wantEnd     time.Time
		wantErr     bool
		errContains string
	}{
		{
			name:       "both empty with defaults",
			s:          "",
			e:          "",
			wantFormat: function.Second,
		},
		{
			name:       "valid second timestamps",
			s:          "1609459200",
			e:          "1609459260",
			wantFormat: function.Second,
			wantStart:  refTime,
			wantEnd:    refTime.Add(time.Minute),
		},
		{
			name:       "valid millisecond timestamps",
			s:          "1609459200000",
			e:          "1609459260000",
			wantFormat: function.Millisecond,
			wantStart:  refTime,
			wantEnd:    refTime.Add(time.Minute),
		},
		{
			name:       "valid microsecond timestamps",
			s:          "1609459200000000",
			e:          "1609459260000000",
			wantFormat: function.Microsecond,
			wantStart:  refTime,
			wantEnd:    refTime.Add(time.Minute),
		},
		{
			name:       "valid nanosecond timestamps",
			s:          "1609459200000000000",
			e:          "1609459260000000000",
			wantFormat: function.Nanosecond,
			wantStart:  refTime,
			wantEnd:    refTime.Add(time.Minute),
		},
		{
			name:        "invalid start format",
			s:           "invalid",
			e:           "1609459200",
			wantErr:     true,
			errContains: "invalid start time",
		},
		{
			name:        "invalid end format",
			s:           "1609459200",
			e:           "invalid",
			wantErr:     true,
			errContains: "invalid end time",
		},
		{
			name:        "format mismatch",
			s:           "1609459200",
			e:           "1609459200000",
			wantErr:     true,
			errContains: "must have the same format",
		},
		{
			name:        "unsupported timestamp length",
			s:           "12345",
			e:           "1609459200",
			wantErr:     true,
			errContains: "unsupported timestamp length",
		},
		{
			name:       "start empty with valid end",
			s:          "",
			e:          "1609459200",
			wantFormat: function.Second,
			wantStart:  refTime,
			wantEnd:    refTime,
		},
		{
			name:       "end empty with valid start",
			s:          "1609459200",
			e:          "",
			wantFormat: function.Second,
			wantStart:  refTime,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unit, start, end, err := function.QueryTimestamp(tt.s, tt.e)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.wantFormat, unit)

			// 处理动态生成的时间（默认值情况）
			switch {
			case tt.s == "" && tt.e == "":
				assert.WithinDuration(t, defaultStart, start, time.Second)
				assert.WithinDuration(t, defaultEnd, end, time.Second)
			case tt.e == "":
				assert.Equal(t, tt.wantStart, start)
				assert.WithinDuration(t, defaultEnd, end, time.Second)
			default:
				assert.Equal(t, tt.wantStart, start)
				assert.Equal(t, tt.wantEnd, end)
			}
		})
	}
}

func TestTimeOffset(t *testing.T) {
	for name, c := range map[string]struct {
		t      time.Time
		tz     string
		step   time.Duration
		actual time.Time
	}{
		"test align": {
			t:      time.Unix(1701306000, 0), // 2023-11-30 09:00:00 +0800 ~ 2024-05-30 09:00:00 +0800
			tz:     "Asia/Shanghai",
			step:   time.Hour * 3,
			actual: time.UnixMilli(1701306000000),
		},
		"test align -1": {
			t:      time.Unix(1703732400, 0), // 2023-11-30 09:00:00 +0800 ~ 2024-05-30 09:00:00 +0800
			tz:     "Asia/Shanghai",
			step:   time.Hour * 3,
			actual: time.UnixMilli(1703725200000),
		},
		"test align - 2": {
			t:      time.Unix(1730082578, 0), // 2024-10-28 10:29:38 +0800 ~ 2024-10-28 10:12:00 +0800
			tz:     "Asia/Shanghai",
			step:   time.Minute * 18,
			actual: time.UnixMilli(1730081520000),
		},
		"test align - 3": {
			t:      time.Unix(1741190400, 0), // 2024-10-28 10:29:38 +0800 ~ 2024-10-28 10:12:00 +0800
			tz:     "Asia/Shanghai",
			step:   time.Hour * 24,
			actual: time.UnixMilli(1741190400000),
		},
		"test alian - 4": {
			t:      time.UnixMilli(1741336672161),
			tz:     "Asia/Shanghai",
			step:   time.Hour * 24,
			actual: time.UnixMilli(1741276800000),
		},
		"test alian - 5": {
			t:      time.UnixMilli(1741336672161),
			tz:     "UTC",
			step:   time.Hour * 24,
			actual: time.UnixMilli(1741305600000),
		},
	} {
		t.Run(name, func(t *testing.T) {
			tz1, t1 := function.TimeOffset(c.t, c.tz, c.step)

			fmt.Println(c.tz, "=>", tz1)
			fmt.Println(c.t.String(), "=>", t1.String())
			fmt.Println(c.t, "=>", t1.Unix())

			assert.Equal(t, c.actual.UnixMilli(), t1.UnixMilli())
		})
	}
}
