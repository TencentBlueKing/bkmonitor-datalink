// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import "time"

// DueClasses names a replica's owned objects by where they stand against their
// due time, taken in the same pass and by the same rule as its ScheduleCensus:
// past due by at most one period, or with no period at all, is late; past due
// by more is overdue; owned with nothing evaluated since takeover is never.
// On-time objects are not listed -- they are the owned objects in no list -- so
// the snapshot grows with what is wrong, not with what it holds.
// The lists and the census beside them cover the replica's owned objects
// only: an object it lost is never named as its own.
//
// Each list is sorted and cut to the store's per-list budget like the owned
// set, and its Cut flag says it was: a cut list is a lower bound, never the
// whole, and "on time = owned minus the lists" holds only when no list and not
// the owned set was cut.
type DueClasses struct {
	Late       []string `json:"late,omitempty"`
	LateCut    bool     `json:"late_cut,omitempty"`
	Overdue    []string `json:"overdue,omitempty"`
	OverdueCut bool     `json:"overdue_cut,omitempty"`
	Never      []string `json:"never,omitempty"`
	NeverCut   bool     `json:"never_cut,omitempty"`
}

// DueClassSource is a ScheduleSource that also names the objects its census
// counts, from one pass: the lists and the counts read one instant.
type DueClassSource interface {
	CensusWithDueClasses(now time.Time, owned []string) (ScheduleCensus, DueClasses)
}
