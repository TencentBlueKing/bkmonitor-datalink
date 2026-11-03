// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import "time"

// InPlaceWaitFacts is one Slot's in-place readiness wait: how many times and
// how long its execution waited, holding its slot, for a later query to
// become readable, how many queries it had sent before its first wait and
// how much of the wait came before any was sent, and what it held
// meanwhile - its retained input exactly, and an estimate (struct sizes) of
// the copies it made for itself. See execution.InPlaceWaitFacts.
type InPlaceWaitFacts struct {
	Waits               uint32
	TotalMillis         int64
	LongestMillis       int64
	SentBeforeFirstWait uint32
	BeforeAnySentMillis int64
	HeldInputBytes      uint64
	HeldPreparedBytes   uint64
}

// InPlaceWaitBuckets is the closed list of the counter's wait label, by an
// execution's total in-place wait.
var InPlaceWaitBuckets = []string{"le5s", "le15s", "le30s", "le60s", "gt60s"}

// InPlaceWaitBucket is the bucket a total in-place wait falls in.
func InPlaceWaitBucket(total time.Duration) string {
	switch {
	case total <= 5*time.Second:
		return "le5s"
	case total <= 15*time.Second:
		return "le15s"
	case total <= 30*time.Second:
		return "le30s"
	case total <= time.Minute:
		return "le60s"
	default:
		return "gt60s"
	}
}
