// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

// InPlaceWaitFacts is how long one execution waited, holding its execution
// slot, for a later query of its Slot to become readable: a Slot whose
// queries are readable at different times is not deferred as a whole, so its
// dispatch waits for each later one in place. Nil on an execution that did
// not wait.
//
// BeforeAnySentMillis is the part of the wait spent before any of the Slot's
// queries was sent: the execution held its slot with nothing of its own
// delivered yet. HeldInputBytes is exact where it is read: the most input the
// execution had retained, from the queries already answered, as the Slot's
// own budget accounting counts it, read at the start and at the end of each
// wait - so an answer that arrived during a wait is counted, and one that came
// and was released inside a wait is not. HeldPreparedBytes is an estimate: the struct sizes of
// what this execution built for itself and keeps across the wait - the
// frozen plan's slices and map copied for it and the prepared queries - not
// what those reference in the cached plan, which is shared and would be held
// without the wait.
type InPlaceWaitFacts struct {
	Waits               uint32
	TotalMillis         int64
	LongestMillis       int64
	SentBeforeFirstWait uint32
	BeforeAnySentMillis int64
	HeldInputBytes      uint64
	HeldPreparedBytes   uint64
}

// RetainedInputReporter is a consumer that can say, from any goroutine, how
// many input bytes the execution has retained so far.
type RetainedInputReporter interface {
	RetainedInputBytesNow() uint64
}
