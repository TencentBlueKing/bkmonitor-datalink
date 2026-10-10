// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package readhold keeps each Query Group's read hold: how much later than
// its schedule says a Slot of it is read and due, for a Query Group whose
// whole window is read before its data has arrived. A Slot carries the hold
// it was frozen with in its contract (execution.FrozenExecutionContractRef).
package readhold

import (
	"encoding/json"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Namespace is the control namespace of a Query Group's read hold record,
// beside its Progress under the schedule prefix.
const Namespace = "read_hold"

// Record is a Query Group's read hold as its owner keeps it: the hold its
// Slots are frozen with now and the first Slot frozen with it, and the hold
// before that and its first Slot. An absent record can also be an expired
// predecessor; it proves no historical hold without independent facts.
type Record struct {
	HoldMillis int64                    `json:"hold_ms"`
	SinceSlot  execution.EvaluationTime `json:"since_slot"`
	// PreviousHoldMillis and PreviousSinceSlot are the hold before and its
	// first Slot. A record begun from no hold has none before it from Slot
	// 1 on: every Slot before SinceSlot was frozen with none.
	PreviousHoldMillis int64                    `json:"previous_hold_ms,omitempty"`
	PreviousSinceSlot  execution.EvaluationTime `json:"previous_since_slot,omitempty"`
	// A change is chosen before its next Slot is known. Freeze moves it into
	// HoldMillis/SinceSlot; until then the previous frozen interval is intact.
	PendingHoldMillis      *int64                   `json:"pending_hold_ms,omitempty"`
	ArrivalAgeMillis       int64                    `json:"arrival_age_ms,omitempty"`
	LastEarlyMillis        int64                    `json:"last_early_ms,omitempty"`
	MaxEarlyIntervalMillis int64                    `json:"max_early_interval_ms,omitempty"`
	QuietSinceMillis       int64                    `json:"quiet_since_ms,omitempty"`
	EarlierMatches         int                      `json:"earlier_matches,omitempty"`
	LastEarlierSlot        execution.EvaluationTime `json:"last_earlier_slot,omitempty"`
	RaisedAfterLowering    uint64                   `json:"raised_after_lowering,omitempty"`
	Lowered                bool                     `json:"lowered,omitempty"`
	Noise                  uint64                   `json:"noise,omitempty"`
	AtLimit                bool                     `json:"at_limit,omitempty"`
	LimitMillis            int64                    `json:"limit_ms,omitempty"`
	Rung                   string                   `json:"rung,omitempty"`
	Buckets                []int64                  `json:"buckets,omitempty"`
	SegmentStart           execution.EvaluationTime `json:"segment_start,omitempty"`
	Closed                 bool                     `json:"closed,omitempty"`
	Plans                  []PlanRecord             `json:"plans,omitempty"`
	Transitions            []Transition             `json:"transitions,omitempty"`
	RenewedAtMillis        int64                    `json:"renewed_at_ms,omitempty"`
}

// ErrRecordInvalid is a record that does not decode or holds out of range.
var ErrRecordInvalid = errors.New("alarmd readhold: invalid read hold record")

// Decode reads a record. Fields it does not know are ignored: a later
// build may keep more beside the holds.
func Decode(raw []byte) (Record, error) {
	var record Record
	if err := json.Unmarshal(raw, &record); err != nil {
		return Record{}, ErrRecordInvalid
	}
	if !inRange(record.HoldMillis) || !inRange(record.PreviousHoldMillis) || record.SinceSlot <= 0 ||
		record.PreviousSinceSlot < 0 || record.PreviousSinceSlot > record.SinceSlot ||
		(record.PendingHoldMillis != nil && !inRange(*record.PendingHoldMillis)) ||
		record.ArrivalAgeMillis < 0 || record.MaxEarlyIntervalMillis < 0 ||
		record.EarlierMatches < 0 || record.EarlierMatches > EarlierMatchesRequired || !inRange(record.LimitMillis) ||
		record.SegmentStart < 0 {
		return Record{}, ErrRecordInvalid
	}
	for _, plan := range record.Plans {
		if plan.Key.PlanIdentity.Validate() != nil || plan.Route == "" || plan.ArrivalAgeMillis < 0 ||
			!inRange(plan.PreviousHoldMillis) || plan.PreviousSlot < 0 || plan.CompletionOffsetMillis < 0 ||
			plan.ClosedAt < 0 || (plan.ClosedAt > 0 && plan.PreviousSlot >= plan.ClosedAt) {
			return Record{}, ErrRecordInvalid
		}
		if plan.InheritedClosedAt < 0 || (plan.InheritedClosedAt > 0 && plan.InheritedQueryGroup == "") {
			return Record{}, ErrRecordInvalid
		}
	}
	for _, transition := range record.Transitions {
		if transition.Key.PlanIdentity.Validate() != nil || transition.DeadlineMillis <= 0 {
			return Record{}, ErrRecordInvalid
		}
	}
	return record, nil
}

// HoldAt is the hold the Slot at slot was frozen with, and false when the
// record no longer says: a Slot before the hold before this one.
func (record Record) HoldAt(slot execution.EvaluationTime) (int64, bool) {
	switch {
	case slot >= record.SinceSlot:
		return record.HoldMillis, true
	case record.PreviousSinceSlot > 0 && slot >= record.PreviousSinceSlot:
		return record.PreviousHoldMillis, true
	}
	return 0, false
}

func inRange(hold int64) bool {
	return hold >= 0 && hold <= execution.MaxReadHoldMillis
}
