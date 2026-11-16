// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"sync"
	"time"
)

// objectRenewalRounds is how many refresh rounds pass between renewals of
// the execution objects and output contexts: every fifth minute at the
// thirty-second refresh. The renewal also finds an object gone - its PEXPIRE
// answers 0 and the catalog forgets it, so the next publication writes it
// again - and it is that finding, not the TTL, that sets the cadence: a day's
// TTL renewed every five minutes is never near running out.
const objectRenewalRounds = 10

// catalogRenewals is what this Control Leader term renewed of the catalog
// and when.
//
// A Schedule timeline is renewed when this term last renewed or wrote it a
// third of the TTL ago or more, or has not. What it remembers holds for
// writes this process made under its own TTL, within one term: another
// process may write a key under a TTL of its own configuration, shorter,
// and a term begins knowing nothing of what came before it, renewing every
// timeline once. A third rather than a half: a store that goes back - a
// Redis failover losing its tail - takes the expiry back to the renewal
// before, and renewals made in one round come due in one round again, so
// one lost at half the TTL would leave the whole batch to run out at the
// next renewal's own moment.
type catalogRenewals struct {
	mu sync.Mutex
	// term is the Control Leader term the memory belongs to (BeginLeaderTerm).
	term         uint64
	timelines    map[string]time.Time
	objectRounds uint64
	clock        func() time.Time
}

func (renewals *catalogRenewals) now() time.Time {
	if renewals.clock != nil {
		return renewals.clock()
	}
	return time.Now()
}

// BeginLeaderTerm gives the catalog's renewals to the Control Leader term
// epoch names: a term other than the one they belong to starts with no
// memory, so its first refresh round renews every timeline, the objects and
// the contexts.
func (repository *RedisCatalogRepository) BeginLeaderTerm(epoch uint64) {
	if repository == nil {
		return
	}
	renewals := &repository.renewals
	renewals.mu.Lock()
	defer renewals.mu.Unlock()
	if renewals.term == epoch {
		return
	}
	renewals.term, renewals.timelines, renewals.objectRounds = epoch, nil, 0
}

// timelinesDue is the keys of keys this term has not renewed or written
// within a third of the TTL: first the ones it has not at all, as many as
// first says, then the ones it did a third of the TTL ago or more. It
// forgets the keys no longer referenced, so what it remembers is bounded by
// the Query Groups the catalog still schedules.
func (renewals *catalogRenewals) timelinesDue(keys []string, ttl time.Duration) (due []string, first int, now time.Time) {
	renewals.mu.Lock()
	defer renewals.mu.Unlock()
	now = renewals.now()
	referenced := make(map[string]time.Time, len(keys))
	aged := make([]string, 0)
	for _, key := range keys {
		last, known := renewals.timelines[key]
		switch {
		case !known:
			due = append(due, key)
		case now.Sub(last) >= ttl/3:
			aged = append(aged, key)
		}
		if known {
			referenced[key] = last
		}
	}
	renewals.timelines = referenced
	first = len(due)
	return append(due, aged...), first, now
}

// renewed notes that keys were renewed or written at the given time, the
// time taken before the call went out. Only a write the store acknowledged
// is noted: a renewal missed leaves a key to be renewed early, a failed one
// noted would leave it to run out.
func (renewals *catalogRenewals) renewed(keys []string, at time.Time) {
	renewals.mu.Lock()
	defer renewals.mu.Unlock()
	if renewals.timelines == nil {
		renewals.timelines = make(map[string]time.Time, len(keys))
	}
	for _, key := range keys {
		renewals.timelines[key] = at
	}
}

// objectRenewalDue says whether this refresh round renews the execution
// objects and output contexts: the first round of the term, and every
// objectRenewalRounds-th after.
func (renewals *catalogRenewals) objectRenewalDue() bool {
	renewals.mu.Lock()
	defer renewals.mu.Unlock()
	due := renewals.objectRounds%objectRenewalRounds == 0
	renewals.objectRounds++
	return due
}

// SetCatalogRenewalClockForTest drives the age of the timeline renewals.
func SetCatalogRenewalClockForTest(repository *RedisCatalogRepository, now func() time.Time) {
	repository.renewals.mu.Lock()
	repository.renewals.clock = now
	repository.renewals.mu.Unlock()
}
