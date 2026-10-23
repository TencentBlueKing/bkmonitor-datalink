// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
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

// A read with exactly its bound left starts; with a millisecond less it
// starts too late; a bound that says nothing never holds a read back.
func TestARoundStartsTooLateOnlyWithLessThanItsBoundLeft(t *testing.T) {
	const deadline = int64(1_700_000_055_000)
	for name, tc := range map[string]struct {
		start int64
		bound time.Duration
		want  bool
	}{
		"exactly the bound left":        {deadline - 5_000, 5 * time.Second, false},
		"a millisecond less":            {deadline - 4_999, 5 * time.Second, true},
		"ample budget":                  {deadline - 30_000, 5 * time.Second, false},
		"at the deadline":               {deadline, 5 * time.Second, true},
		"no bound":                      {deadline - 1, 0, false},
		"a negative bound says nothing": {deadline - 1, -time.Second, false},
	} {
		if got := StartsTooLate(tc.start, deadline, tc.bound); got != tc.want {
			t.Errorf("%s: StartsTooLate = %v, want %v", name, got, tc.want)
		}
	}
}
