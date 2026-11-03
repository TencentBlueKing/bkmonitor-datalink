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
	"maps"
	"time"
)

// RetainedHoldersListed bounds the Slots one snapshot names as holding the
// retained pool; Total counts them all.
const RetainedHoldersListed = 5

// RetainedHolderStrategiesListed bounds the strategies named for one holder;
// StrategiesTotal counts them all.
const RetainedHolderStrategiesListed = 4

// RetainedHolderFacts is what held this replica's retained pool as it
// published: the Slots holding the most, the one holding the longest, and
// the pool's own figure beside what the Slots add up to. The usage is a
// metric too; only the snapshot can name who holds it.
type RetainedHolderFacts struct {
	// Holders is the Slots holding the most, the largest first, at most
	// RetainedHoldersListed of them.
	Holders []RetainedHolderRef `json:"holders"`
	// Oldest is the Slot that has held the longest, listed or not. Absent
	// when nothing is held.
	Oldest *RetainedHolderRef `json:"oldest,omitempty"`
	// Total is how many Slots hold anything.
	Total int `json:"total"`
	// AttributedBytes is what every holding Slot adds up to, listed or not;
	// HeldBytes is the pool's own figure, read just after. A Slot starting
	// or ending between the two moves one first, so they differ for a
	// moment; HeldBytes above AttributedBytes snapshot after snapshot is a
	// reservation no Slot names.
	AttributedBytes uint64 `json:"attributed_bytes"`
	HeldBytes       uint64 `json:"held_bytes"`
}

// RetainedHolderRef is one Slot's hold on the retained pool: the object and
// the round it runs, the strategies this replica has seen it evaluate, since
// when the Slot holds, and how many bytes for what -- the stream's input,
// gap facts, outputs and state, and the frames its Plans keep for their
// writes.
type RetainedHolderRef struct {
	QueryGroup      string            `json:"query_group"`
	EvaluationTime  int64             `json:"evaluation_time"`
	Operation       string            `json:"operation"`
	Strategies      []StrategyRef     `json:"strategies"`
	StrategiesTotal int               `json:"strategies_total"`
	Since           time.Time         `json:"since"`
	Bytes           uint64            `json:"bytes"`
	ByKind          map[string]uint64 `json:"by_kind"`
}

// NameRetainedHolder fills in a holder's strategies from what this replica
// has seen the object evaluate, the first RetainedHolderStrategiesListed of
// them; none is an empty list, for an object it has not seen finish a round.
func NameRetainedHolder(ref RetainedHolderRef, strategies []StrategyRef) RetainedHolderRef {
	ref.StrategiesTotal = len(strategies)
	if len(strategies) > RetainedHolderStrategiesListed {
		strategies = strategies[:RetainedHolderStrategiesListed]
	}
	ref.Strategies = append([]StrategyRef{}, strategies...)
	return ref
}

// CopyRetainedHolderFacts is the facts with their own lists and maps, or nil,
// so a later read cannot alias the snapshot's.
func CopyRetainedHolderFacts(facts *RetainedHolderFacts) *RetainedHolderFacts {
	if facts == nil {
		return nil
	}
	copied := *facts
	copied.Holders = make([]RetainedHolderRef, 0, len(facts.Holders))
	for _, holder := range facts.Holders {
		copied.Holders = append(copied.Holders, copyRetainedHolderRef(holder))
	}
	if facts.Oldest != nil {
		oldest := copyRetainedHolderRef(*facts.Oldest)
		copied.Oldest = &oldest
	}
	return &copied
}

func copyRetainedHolderRef(ref RetainedHolderRef) RetainedHolderRef {
	ref.Strategies = append([]StrategyRef{}, ref.Strategies...)
	ref.ByKind = maps.Clone(ref.ByKind)
	return ref
}
