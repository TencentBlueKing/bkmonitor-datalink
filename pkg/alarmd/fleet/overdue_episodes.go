// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"sort"
	"time"
)

// MaxOverdueEpisodes bounds the overdue episodes a replica keeps and a
// reader lists: the latest, which at a few a day reach back days, past the
// logs that would otherwise be the only record of them -- while the replica
// lives. They are kept in its memory, so a restart or a rollout starts them
// empty, and an object handed over ends on one replica and begins on the
// next. Each is about 400 bytes encoded with one strategy and 48 more for
// each further one, at most the tracker's 32: a full list adds about 25 KB
// to every snapshot the replica writes, and at most about 120 KB.
const MaxOverdueEpisodes = 64

// OverdueObject is an object of a replica's rows whose schedule the replica
// found overdue when it decided them: the line SLOTS_OVERDUE counts, by
// object, where only this replica still knows which.
type OverdueObject struct {
	QueryGroup      string        `json:"query_group"`
	Strategies      []StrategyRef `json:"strategies,omitempty"`
	DueAt           time.Time     `json:"due_at,omitempty"`
	IntervalSeconds int64         `json:"interval_seconds,omitempty"`
}

// OverdueEpisode is one object's time overdue on one replica: from the first
// publish that found it overdue to the first that did not, with its read hold
// when it began -- known or not -- so an overdue object says whether its
// Slots were held.
type OverdueEpisode struct {
	OverdueObject
	Replica        string    `json:"replica"`
	Onset          time.Time `json:"onset"`
	Clear          time.Time `json:"clear"`
	ReadHoldMillis int64     `json:"read_hold_ms"`
	ReadHoldKnown  bool      `json:"read_hold_known"`
}

// HoldClass is the episode's read hold as the episode counter labels it:
// positive, zero, or unknown.
func (episode OverdueEpisode) HoldClass() string {
	switch {
	case !episode.ReadHoldKnown:
		return "unknown"
	case episode.ReadHoldMillis > 0:
		return "positive"
	default:
		return "zero"
	}
}

// OverdueHoldClasses is every HoldClass.
var OverdueHoldClasses = []string{"zero", "positive", "unknown"}

// overdueObjectsOf is the objects of the rows under SLOTS_OVERDUE, each once,
// by identity.
func overdueObjectsOf(columns [][]Anomaly) []OverdueObject {
	seen := map[string]bool{}
	var objects []OverdueObject
	for _, column := range columns {
		for _, row := range column {
			if row.Finding.Check != CheckSlotsOverdue || seen[row.QueryGroup] {
				continue
			}
			seen[row.QueryGroup] = true
			object := OverdueObject{QueryGroup: row.QueryGroup, Strategies: append([]StrategyRef(nil), row.Strategies...)}
			if row.Wake != nil {
				object.DueAt, object.IntervalSeconds = row.Wake.DueAt, row.Wake.IntervalSeconds
			}
			objects = append(objects, object)
		}
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].QueryGroup < objects[j].QueryGroup })
	return objects
}

// latestOverdueEpisodes is the episodes the latest-cleared first, at most
// MaxOverdueEpisodes.
func latestOverdueEpisodes(episodes []OverdueEpisode) []OverdueEpisode {
	sorted := append([]OverdueEpisode(nil), episodes...)
	sort.Slice(sorted, func(i, j int) bool {
		if !sorted[i].Clear.Equal(sorted[j].Clear) {
			return sorted[i].Clear.After(sorted[j].Clear)
		}
		if sorted[i].Replica != sorted[j].Replica {
			return sorted[i].Replica < sorted[j].Replica
		}
		return sorted[i].QueryGroup < sorted[j].QueryGroup
	})
	if len(sorted) > MaxOverdueEpisodes {
		sorted = sorted[:MaxOverdueEpisodes]
	}
	return sorted
}
