// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openalerts

// This process keeps two records of what it sent. added holds the alerts
// whose ABNORMAL it sent within the local retention -- an alert still firing
// is sent again every round and stays; one that stops being sent leaves at
// the next calibration after the retention, whether or not it recovered.
// index.opened holds every alert it opened and has not sent the RECOVERY
// for, and is what makes an alert "own" at the gate long after it left
// added. A count that falls in added with no RECOVERY past the gate read as
// alerts leaving without recovering, when it was alerts no longer re-sent
// that are still open; counting each departure by its path tells the two
// apart.

// Why an alert left one of the two records, closed.
const (
	// DepartureRecoveryAcked is a RECOVERY for the alert that the broker took.
	DepartureRecoveryAcked = "recovery_acked"
	// DepartureNotResent is an alert whose ABNORMAL was not sent again within
	// the local retention, pruned from added by a calibration or a refresh.
	// It says nothing about whether the alert is still open.
	DepartureNotResent = "not_resent"
	// DepartureUntracked is an alert whose strategy left this process's
	// tracked scope: removed from the source, or owned by another replica.
	DepartureUntracked = "untracked"
	// DepartureEvicted is an alert added gave up to stay inside its bound.
	DepartureEvicted = "evicted"
)

// SentDepartures is every path out of added.
var SentDepartures = []string{DepartureRecoveryAcked, DepartureNotResent, DepartureUntracked, DepartureEvicted}

// OwnOpenDepartures is every path out of index.opened: a RECOVERY, or the
// strategy leaving. Being no longer re-sent is not one of them.
var OwnOpenDepartures = []string{DepartureRecoveryAcked, DepartureUntracked}

// leaveSent removes m from added and counts why. Called with the lock held.
func (cache *Cache) leaveSent(m member, path string) {
	if _, ok := cache.added[m]; !ok {
		return
	}
	delete(cache.added, m)
	if cache.sentDepartures == nil {
		cache.sentDepartures = map[string]uint64{}
	}
	cache.sentDepartures[path]++
}

// leaveOpen removes m from index.opened and counts why. Called with the lock
// held; a no-op outside the index protocol.
func (cache *Cache) leaveOpen(m member, path string) {
	if cache.index == nil {
		return
	}
	if _, ok := cache.index.opened[m]; !ok {
		return
	}
	delete(cache.index.opened, m)
	if cache.openDepartures == nil {
		cache.openDepartures = map[string]uint64{}
	}
	cache.openDepartures[path]++
}

// departureStats copies the departures into stats. Called with the lock held.
func (cache *Cache) departureStats(stats *Stats) {
	stats.SentDepartures = make(map[string]uint64, len(SentDepartures))
	for _, path := range SentDepartures {
		stats.SentDepartures[path] = cache.sentDepartures[path]
	}
	if cache.index == nil {
		return
	}
	stats.OwnOpenKnown = true
	stats.OwnOpen = len(cache.index.opened)
	stats.OwnOpenRefusals = cache.openRefusals
	stats.OwnOpenDepartures = make(map[string]uint64, len(OwnOpenDepartures))
	for _, path := range OwnOpenDepartures {
		stats.OwnOpenDepartures[path] = cache.openDepartures[path]
	}
}
