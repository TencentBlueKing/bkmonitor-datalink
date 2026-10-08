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
	"fmt"
	"sort"
	"time"
)

// ReadHeld is a row for every object whose reads alarmd holds on a measured
// arrival age, with the strategies this process has seen evaluate on it, the
// furthest from its suggested time_delay first. Its rounds complete and read
// the data whole, later than the time_delay says; the time_delay that would
// need no hold is the strategy owner's to set, and the row is how the owner
// is told while the hold stands in for it. A hold that rests on no
// measurement, or not yet frozen into a Slot, has no row.
func (tracker *Tracker) ReadHeld(holds map[string]ReadHoldFacts) []Anomaly {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	var anomalies []Anomaly
	for queryGroup, facts := range holds {
		if facts.Millis <= 0 || facts.SuggestedDelaySeconds <= 0 || facts.HeldSince <= 0 {
			continue
		}
		state := tracker.groups[queryGroup]
		if state == nil {
			// Never seen evaluate here: no strategy to name it under.
			continue
		}
		facts := facts
		facts.Buckets = nil
		anomaly := Anomaly{
			QueryGroup: queryGroup, Kind: KindReadHeld,
			Since: time.Unix(facts.HeldSince, 0).UTC(), SinceFrom: SinceBusinessState, Replica: tracker.replica,
			ReadHold: &facts, LastHealthyAt: state.lastHealthyAt,
		}
		for strategy := range state.strategies {
			anomaly.Strategies = append(anomaly.Strategies, strategy)
		}
		sortStrategies(anomaly.Strategies)
		anomalies = append(anomalies, anomaly)
	}
	sort.Slice(anomalies, func(left, right int) bool {
		l, r := anomalies[left].ReadHold, anomalies[right].ReadHold
		if lg, rg := l.SuggestedDelaySeconds-l.DelaySeconds, r.SuggestedDelaySeconds-r.DelaySeconds; lg != rg {
			return lg > rg
		}
		return anomalies[left].QueryGroup < anomalies[right].QueryGroup
	})
	return anomalies
}

// withHold is the advice with one more of the strategy's objects' read holds
// taken in. A held object makes the advice say alarmd holds its reads, the
// longest hold of them; one whose hold rests on a measured arrival age also
// offers the time_delay that would need none, taken as a read-early row's
// suggestion is: the largest, and among equal ones the earlier. A hold not
// yet frozen into a Slot is not one yet.
func (advice *TimeDelayAdvice) withHold(queryGroup string, facts *ReadHoldFacts) *TimeDelayAdvice {
	if facts == nil || facts.Millis <= 0 || facts.HeldSince <= 0 {
		return advice
	}
	if advice == nil {
		advice = &TimeDelayAdvice{objects: map[string]struct{}{}}
	}
	advice.ReadHoldSeconds = max(advice.ReadHoldSeconds, heldSeconds(facts.Millis))
	since := time.Unix(facts.HeldSince, 0).UTC()
	if facts.SuggestedDelaySeconds <= 0 {
		if advice.Object == "" {
			advice.CurrentDelaySeconds, advice.Object, advice.Since = facts.DelaySeconds, queryGroup, since
		}
		return advice
	}
	advice.objects[queryGroup] = struct{}{}
	advice.Objects = len(advice.objects)
	if advice.SuggestedDelaySeconds > 0 && (facts.SuggestedDelaySeconds < advice.SuggestedDelaySeconds ||
		(facts.SuggestedDelaySeconds == advice.SuggestedDelaySeconds && !since.Before(advice.Since))) {
		return advice
	}
	advice.CurrentDelaySeconds, advice.SuggestedDelaySeconds = facts.DelaySeconds, facts.SuggestedDelaySeconds
	advice.Object, advice.Since, advice.Samples, advice.PartialRevised = queryGroup, since, 0, 0
	return advice
}

// heldSeconds is a hold in whole seconds, to the nearest.
func heldSeconds(millis int64) int64 {
	return (millis + 500) / 1000
}

// readHeldClause is the sentence a READ_HELD row adds to its strategy's
// line: how late the data has been seen to arrive -- the arrival age is the
// latest seen, not a typical one -- what alarmd holds, and the time_delay
// that would need no hold, with the settling wait alarmd adds after it: the
// time_delay suggested is net of it, and can read as less than the arrival.
func readHeldClause(facts *ReadHoldFacts) string {
	if facts == nil {
		return ""
	}
	clause := fmt.Sprintf("数据最晚约在窗口结束后 %d 秒到齐，alarmd 当前自动推后 %d 秒；建议把 time_delay 改为 %d 秒",
		heldSeconds(facts.ArrivalAgeMillis), heldSeconds(facts.Millis), facts.SuggestedDelaySeconds)
	if facts.SettlingWaitSeconds > 0 {
		clause += fmt.Sprintf("（alarmd 另有约 %d 秒就绪等待）", facts.SettlingWaitSeconds)
	}
	return clause
}
