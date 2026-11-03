// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"container/list"
	"slices"
	"sync"
)

// maxReasonsPerSlot bounds the reasons a Query Group's Slot is remembered
// with. A Slot's series end with a handful between them; one with more keeps
// the first so many, and a reason past them is not one its next Slot can be
// said to have changed from.
const maxReasonsPerSlot = 16

// reasonTransitions is what the policy remembers to write the line on which
// a Query Group's evaluation reason changed: per group, the reasons its
// normal evaluations ended with in its newest Slot and in the Slot before.
//
// An evaluation whose reason the group's previous Slot did not have is the
// round the group changed on - into its hours or out of them, warming to
// whole, whole to a gap - and its line is written whatever its result. Under
// the hourly sample that line is not the one the sample keeps, and a
// success is not written at all, so the boundary was never in the log: a
// reading of when a group's hours began found its last out-of-hours sample
// and nothing after it.
//
// One line per group per Slot at most, the first of its new reasons. A
// group this process has seen one Slot of, newly owned or after a restart,
// has no Slot to have changed from and writes none until its second. A Slot
// older than the newest - a supplement's, a replay's - is not a step of the
// group's own sequence and changes nothing. Groups are bounded by max, the
// least recently evaluated forgotten first.
type reasonTransitions struct {
	mu     sync.Mutex
	max    int
	groups map[string]*list.Element
	order  *list.List
}

type groupReasons struct {
	group string
	slot  int64
	// reasons are the newest Slot's, previous the Slot's before it; seen
	// says previous is a Slot this process evaluated.
	reasons  []ReasonCode
	previous []ReasonCode
	seen     bool
	// written says the newest Slot has had its line.
	written bool
}

func newReasonTransitions(max int) *reasonTransitions {
	return &reasonTransitions{max: max, groups: make(map[string]*list.Element), order: list.New()}
}

// reasonTransitionCandidate is an observation reasonTransitions follows: a
// normal evaluation's completion, of a Slot of a Query Group.
func reasonTransitionCandidate(observation Observation) bool {
	return observation.Component == ComponentEvaluation && observation.Stage == StageEvaluationCompleted &&
		observation.Operation == OperationNormal && observation.Trace.QueryGroupKey != "" && observation.Trace.EvaluationTime > 0
}

// observe notes the reason one evaluation of the group's Slot ended with,
// and says whether it is the group's change: the first reason in the Slot
// that the Slot before it did not have, with the reasons that Slot had.
func (t *reasonTransitions) observe(group string, slot int64, reason ReasonCode) (bool, []ReasonCode) {
	if t == nil || t.max <= 0 {
		return false, nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.entry(group)
	switch {
	case entry.slot == 0:
		entry.slot = slot
	case slot > entry.slot:
		entry.previous, entry.seen = entry.reasons, true
		entry.reasons, entry.slot, entry.written = nil, slot, false
	case slot < entry.slot:
		return false, nil
	}
	if slices.Contains(entry.reasons, reason) {
		return false, nil
	}
	if len(entry.reasons) < maxReasonsPerSlot {
		entry.reasons = append(entry.reasons, reason)
	}
	if !entry.seen || entry.written || slices.Contains(entry.previous, reason) {
		return false, nil
	}
	entry.written = true
	return true, slices.Clone(entry.previous)
}

func (t *reasonTransitions) entry(group string) *groupReasons {
	if element, ok := t.groups[group]; ok {
		t.order.MoveToFront(element)
		return element.Value.(*groupReasons)
	}
	for t.order.Len() >= t.max {
		oldest := t.order.Back()
		if oldest == nil {
			break
		}
		t.order.Remove(oldest)
		delete(t.groups, oldest.Value.(*groupReasons).group)
	}
	entry := &groupReasons{group: group}
	t.groups[group] = t.order.PushFront(entry)
	return entry
}
