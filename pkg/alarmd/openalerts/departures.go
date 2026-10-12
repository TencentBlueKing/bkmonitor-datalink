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
// index.opened holds every alert it opened until a trusted set shows it
// closed or its RECOVERY has been sent for a grace period, and is what makes
// an alert "own" at the gate long after it left added. A RECOVERY does not
// take an alert out at once: the consumer closes an alert only at the Level
// it stands at, and the set says which closed. Counting each departure by its path tells an alert no longer
// re-sent from one the set no longer carries.

// Why an alert left one of the two records, closed.
const (
	// DepartureNotInSet is an alert of ours a trusted set's calibration no
	// longer lists, and that this process has not sent the ABNORMAL for
	// within the local retention: the consumer closed it.
	DepartureNotInSet = "not_in_set"
	// DepartureRecovered is an alert whose RECOVERY the broker took a grace
	// period ago with no ABNORMAL since, in either trust state.
	DepartureRecovered = "recovered"
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
var SentDepartures = []string{DepartureNotResent, DepartureUntracked, DepartureEvicted}

// OwnOpenDepartures is every path out of index.opened: the set showing the
// alert closed, its recovery sent a grace period ago, the strategy leaving,
// or room made for a new alert in a full record. Being no longer re-sent is
// not one of them.
var OwnOpenDepartures = []string{DepartureNotInSet, DepartureRecovered, DepartureUntracked, DepartureEvicted}

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
// held.
func (cache *Cache) leaveOpen(m member, path string) {
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
	stats.OwnOpen = len(cache.index.opened)
	stats.OwnOpenDepartures = make(map[string]uint64, len(OwnOpenDepartures))
	for _, path := range OwnOpenDepartures {
		stats.OwnOpenDepartures[path] = cache.openDepartures[path]
	}
}
