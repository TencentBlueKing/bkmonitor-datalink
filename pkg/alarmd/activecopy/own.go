// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package activecopy

import (
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// Own is the recovery gate's answer on a deployment whose alerts do not go
// to the link (no phase_two.linkd block): the record of the alerts this
// process opened, and nothing else. There is no link to search, so the
// Copy above is not built; without an answer at all the gate would hold
// every RECOVERY, the alerts this process opened included, and they would
// stay open for ever. Passing every RECOVERY instead is not the answer
// either: a healthy series says RECOVERY every cycle, and every one of them
// would reach the consumer as an orphan.
//
// A RECOVERY passes for an alert whose ABNORMAL this process sent and the
// broker took, until the consumer has ended it; anything else is held. The
// consumer keeps an alert at one standing level - the most severe level
// triggered since it opened - and ends it only on a RECOVERY at that level;
// a RECOVERY at any other level, or any after the end, is an orphan there.
// That is the link's rule (lifecycle preparePlan); where Own runs, the
// consumer is the platform's own alert pipeline (config.LinkdConfig.Present),
// and the same rule is assumed of it: a pipeline that ended alerts later or
// at another level would show as held_no_open_alert on series it still
// holds; one that ended them on any level's RECOVERY would receive the later
// RECOVERYs as orphans.
// So the record keeps the standing level the same way: an ABNORMAL raises
// it to its primary level (the most severe one it triggers), and one that
// also says RECOVERY at the standing level starts the alert again at its
// primary, as the consumer rotates it. The broker taking a RECOVERY at the
// standing level ends the record, and the series' later RECOVERYs are held.
//
// A RECOVERY without the standing level ends nothing at the consumer, and
// the standing level may never say RECOVERY at all (a level edited out of
// the strategy). The first such RECOVERY starts RecoveredGrace, after which
// the record goes: the bound the self-maintained index (openalerts
// indexGate, PolicySelfMaintain, with no Console) put on every alert.
//
// A strategy tracked again after untrackedGap starts from nothing, as the
// Copy does: another owner may have opened or closed its alerts meanwhile.
// At MaxEntries the alert whose grace runs goes first, then the one sent
// longest ago.
type Own struct {
	now     func() time.Time
	mu      sync.Mutex
	opened  map[member]ownRecord
	tracked map[StrategyKey]time.Time
	// pruned is when Track last let the expired records go: at most once a
	// minute, not once a Slot.
	pruned time.Time
}

// RecoveredGrace is how long an alert this process opened still passes its
// RECOVERY after the broker took the first one that did not carry its
// standing level: the rounds of the grace give the standing level's
// RECOVERY its turn. The self-maintained index's grace, its calibration
// interval.
const RecoveredGrace = 30 * time.Minute

type ownRecord struct {
	lastSent, recoveredAt time.Time
	standing              standingLevel
}

// standingLevel is the level the consumer keeps the alert at.
type standingLevel struct {
	id, priority uint32
}

// moreSevere orders levels as the primary level is chosen: lower priority,
// then lower level id.
func (l standingLevel) moreSevere(than standingLevel) bool {
	return l.priority < than.priority || (l.priority == than.priority && l.id < than.id)
}

// NewOwn builds the record; now is the process clock (time.Now when nil).
func NewOwn(now func() time.Time) *Own {
	if now == nil {
		now = time.Now
	}
	return &Own{now: now, opened: map[member]ownRecord{}, tracked: map[StrategyKey]time.Time{}}
}

// Contains is the recovery gate's question (contract.OpenAlertSet).
func (o *Own) Contains(tenantID, strategyID, fingerprint string) bool {
	if o == nil {
		return false
	}
	m := member{key: StrategyKey{TenantID: tenantID, StrategyID: strategyID}, fingerprint: fingerprint}
	now := o.now()
	o.mu.Lock()
	defer o.mu.Unlock()
	record, ok := o.opened[m]
	if !ok {
		return false
	}
	if !record.recoveredAt.IsZero() && now.Sub(record.recoveredAt) > RecoveredGrace {
		delete(o.opened, m)
		return false
	}
	return true
}

// Acknowledged takes the envelopes the broker acknowledged; see Own for
// what each does to the record. Only envelopes with a fingerprint on the
// consumer's protocol count, as in Copy.Acknowledged.
func (o *Own) Acknowledged(events []contract.TriggerEventV1) {
	if o == nil || len(events) == 0 {
		return
	}
	now := o.now()
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, event := range events {
		if event.DedupeMD5 == "" || event.StrategyRef == nil || event.LegacyOutput != nil {
			continue
		}
		m := member{key: StrategyKey{TenantID: event.TenantID, StrategyID: event.PlanRef.StrategyID}, fingerprint: event.DedupeMD5}
		record, ok := o.opened[m]
		switch event.EventKind {
		case contract.TriggerEventAbnormal:
			primary, found := levelOf(event, event.PrimaryLevelID)
			if !found {
				// Unreachable on a built event: the primary is one of its levels.
				continue
			}
			if !ok && len(o.opened) >= MaxEntries {
				o.evictStalestLocked(now)
			}
			standing := primary
			if ok && !recoversAt(event, record.standing) && record.standing.moreSevere(primary) {
				standing = record.standing
			}
			o.opened[m] = ownRecord{lastSent: now, standing: standing}
		case contract.TriggerEventRecovery:
			switch {
			case !ok:
			case recoversAt(event, record.standing):
				delete(o.opened, m)
			case record.recoveredAt.IsZero():
				record.recoveredAt = now
				o.opened[m] = record
			}
		}
	}
}

// levelOf finds one of the event's levels.
func levelOf(event contract.TriggerEventV1, id uint32) (standingLevel, bool) {
	for _, level := range event.LevelResults {
		if level.LevelID == id {
			return standingLevel{id: level.LevelID, priority: level.Priority}, true
		}
	}
	return standingLevel{}, false
}

// recoversAt says the event ends the alert at the consumer: RECOVERY at its
// standing level, written resolved or closed.
func recoversAt(event contract.TriggerEventV1, standing standingLevel) bool {
	for _, level := range event.LevelResults {
		if level.LevelID == standing.id && level.Result == contract.LevelResultRecovery {
			return true
		}
	}
	return false
}

// Track notes the strategies this process evaluates; see Own.
func (o *Own) Track(keys ...StrategyKey) {
	if o == nil {
		return
	}
	now := o.now()
	o.mu.Lock()
	defer o.mu.Unlock()
	var dropped map[StrategyKey]bool
	for _, key := range keys {
		if last, ok := o.tracked[key]; ok && now.Sub(last) > untrackedGap {
			if dropped == nil {
				dropped = map[StrategyKey]bool{}
			}
			dropped[key] = true
		}
		o.tracked[key] = now
	}
	expire := now.Sub(o.pruned) >= time.Minute
	if dropped == nil && !expire {
		return
	}
	if expire {
		o.pruned = now
	}
	for m, record := range o.opened {
		if dropped[m.key] || (expire && !record.recoveredAt.IsZero() && now.Sub(record.recoveredAt) > RecoveredGrace) {
			delete(o.opened, m)
		}
	}
}

// Known is how many alerts the record holds.
func (o *Own) Known() int {
	if o == nil {
		return 0
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.opened)
}

// evictStalestLocked lets one alert go to make room: an expired one, else
// one whose RECOVERY was sent, else the one sent longest ago.
func (o *Own) evictStalestLocked(now time.Time) {
	var stalest member
	var at ownRecord
	found := false
	for m, record := range o.opened {
		if !record.recoveredAt.IsZero() && now.Sub(record.recoveredAt) > RecoveredGrace {
			delete(o.opened, m)
			return
		}
		if !found || ownStaler(record, at) {
			stalest, at, found = m, record, true
		}
	}
	if found {
		delete(o.opened, stalest)
	}
}

func ownStaler(a, b ownRecord) bool {
	if a.recoveredAt.IsZero() != b.recoveredAt.IsZero() {
		return !a.recoveredAt.IsZero()
	}
	return a.lastSent.Before(b.lastSent)
}
