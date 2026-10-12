// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"testing"
	"time"
)

// The suggestion is what the data needed, in whole seconds, rounded up to
// the unit the time_delay is written in; nothing when the current one
// already covers it.
func TestTheSuggestedTimeDelayIsTheNeedRoundedUpToItsUnit(t *testing.T) {
	for _, tc := range []struct {
		need, current, unit time.Duration
		want                int64
	}{
		{need: 100 * time.Second, current: 100 * time.Second, unit: time.Minute, want: 0},
		{need: 90 * time.Second, current: 100 * time.Second, unit: time.Minute, want: 0},
		{need: 100*time.Second + time.Millisecond, current: 100 * time.Second, unit: 0, want: 101},
		{need: 101 * time.Second, current: 60 * time.Second, unit: time.Minute, want: 120},
		{need: 120 * time.Second, current: 60 * time.Second, unit: time.Minute, want: 120},
		{need: 61 * time.Second, current: 0, unit: 30 * time.Second, want: 90},
		{need: 61 * time.Second, current: 0, unit: 500 * time.Millisecond, want: 61},
	} {
		if got := SuggestedTimeDelaySeconds(tc.need, tc.current, tc.unit); got != tc.want {
			t.Errorf("need %v current %v unit %v: got %d, want %d", tc.need, tc.current, tc.unit, got, tc.want)
		}
	}
}
