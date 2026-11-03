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
// It answers as the self-maintained index did before the link copy existed
// (openalerts indexGate, PolicySelfMaintain, with no Console): a RECOVERY
// passes for an alert whose ABNORMAL this process sent and the broker took,
// from that send until RecoveredGrace after the first RECOVERY the broker
// took since it; anything else is held. A strategy tracked again after
// untrackedGap starts from nothing, as the Copy does: another owner may have
// opened or closed its alerts meanwhile. At MaxEntries the alert whose
// RECOVERY was already sent goes first, then the one sent longest ago.
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
// RECOVERY after the broker took the first one: the consumer closes it at
// the Level it stands at, and the rounds of the grace give every Level's
// RECOVERY its turn. The self-maintained index's grace, its calibration
// interval.
const RecoveredGrace = 30 * time.Minute

type ownRecord struct {
	lastSent, recoveredAt time.Time
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

// Acknowledged takes the envelopes the broker acknowledged: an ABNORMAL
// opens or renews the alert and clears a RECOVERY sent before it; the first
// RECOVERY since starts the grace. Only envelopes with a fingerprint on the
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
		switch event.EventKind {
		case contract.TriggerEventAbnormal:
			if _, ok := o.opened[m]; !ok && len(o.opened) >= MaxEntries {
				o.evictStalestLocked(now)
			}
			o.opened[m] = ownRecord{lastSent: now}
		case contract.TriggerEventRecovery:
			if record, ok := o.opened[m]; ok && record.recoveredAt.IsZero() {
				record.recoveredAt = now
				o.opened[m] = record
			}
		}
	}
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
