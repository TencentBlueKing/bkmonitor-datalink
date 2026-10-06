// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"testing"
	"time"
)

// awaitTestSignal waits on a channel with a bound taken from the test deadline
// rather than a fixed sleep. It was defined with the consumer service tests and
// used by the publisher ones; it moved here when that service was retired.
func awaitTestSignal[T any](t testing.TB, signal <-chan T, description string) T {
	t.Helper()
	wait := 30 * time.Second
	if deadlineTest, ok := t.(interface{ Deadline() (time.Time, bool) }); ok {
		if deadline, hasDeadline := deadlineTest.Deadline(); hasDeadline {
			remaining := time.Until(deadline) - time.Second
			if remaining < wait {
				wait = remaining
			}
		}
	}
	if wait <= 0 {
		var zero T
		t.Fatalf("test deadline reached while waiting for %s", description)
		return zero
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case value := <-signal:
		return value
	case <-timer.C:
		var zero T
		t.Fatalf("timed out waiting for %s", description)
		return zero
	}
}
