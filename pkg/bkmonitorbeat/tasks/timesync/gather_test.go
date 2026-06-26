// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package timesync

import "testing"

// 全负偏移（本地时钟超前 NTP 源，即"时间调至未来"）时，Max 应取最接近 0 的负值，
// 而不是历史 bug 中被钳在 0。
func TestStatAddNegativeOffsets(t *testing.T) {
	s := newStat("ntpd")
	s.Add(-3.5)
	s.Add(-1.2)
	if s.Max != -1.2 {
		t.Fatalf("Max = %v, want -1.2 (must not clamp at 0)", s.Max)
	}
	if s.Min != -3.5 {
		t.Fatalf("Min = %v, want -3.5", s.Min)
	}
}

// 单样本时 Min == Max == 样本值，杜绝出现 max < min 这类自相矛盾。
func TestStatAddSingleSampleConsistent(t *testing.T) {
	s := newStat("chrony")
	s.Add(-2.0)
	if s.Min != -2.0 || s.Max != -2.0 {
		t.Fatalf("single negative sample: min=%v max=%v, want both -2.0", s.Min, s.Max)
	}
}

// Count == 0：不得把哨兵初值（±MaxFloat64）或 NaN 当指标发出，min/max/avg 统一为 0。
func TestStats2MetricsEmptyNoSentinelLeak(t *testing.T) {
	m := stats2Metrics("test", newStat("ntpd")).Metrics
	for _, k := range []string{
		"test_timesync_query_seconds_min",
		"test_timesync_query_seconds_max",
		"test_timesync_query_seconds_avg",
	} {
		v, ok := m[k]
		if !ok {
			t.Fatalf("missing metric %s", k)
		}
		// v != 0 同时覆盖哨兵值（±1.8e308）与 NaN（NaN != 0 恒为 true）。
		if v != 0 {
			t.Fatalf("%s = %v, want 0 (no sentinel/NaN leak)", k, v)
		}
	}
	if m["test_timesync_query_count"] != 0 {
		t.Fatalf("count = %v, want 0", m["test_timesync_query_count"])
	}
}

// Count > 0 且偏移为负：min/max/avg 应如实反映负值，max 不被钳在 0。
func TestStats2MetricsNegativeOffset(t *testing.T) {
	s := newStat("ntpd")
	s.Add(-3.5)
	m := stats2Metrics("test", s).Metrics
	if got := m["test_timesync_query_seconds_max"]; got != -3.5 {
		t.Fatalf("max = %v, want -3.5", got)
	}
	if got := m["test_timesync_query_seconds_min"]; got != -3.5 {
		t.Fatalf("min = %v, want -3.5", got)
	}
	if got := m["test_timesync_query_seconds_avg"]; got != -3.5 {
		t.Fatalf("avg = %v, want -3.5", got)
	}
}
