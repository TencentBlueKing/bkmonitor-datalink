// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import "time"

// StartsTooLate says a normal read that would start at startUnixMilli cannot
// finish before its frozen query deadline: less than bound, the time a read of
// its query takes, is left between them. A read started then is cut off at
// the deadline with nothing to show, and the round is lost; one that has
// bound or more left runs as any round does. A bound of zero or less says
// nothing about the query, and nothing starts too late under it.
//
// It is the one rule the scheduler applies wherever a round is about to start
// short of time: a round taken over late in its period, and a round whose
// readiness - the Slot's settling wait and its read hold, which the runner
// waits for before it asks the source again - lands at the edge of its
// deadline.
func StartsTooLate(startUnixMilli, deadlineUnixMilli int64, bound time.Duration) bool {
	if bound <= 0 {
		return false
	}
	return deadlineUnixMilli-startUnixMilli < bound.Milliseconds()
}
