// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdbcache

// HostBusinessLookup answers which business a host identity belongs to, from
// whichever index snapshot is current when it is asked.
//
// It reads Current() per call rather than holding an Index. A Slot that held
// one would answer from the snapshot it started with, which is the right thing
// for the series inside one round; but this is asked once per Plan and the
// question is "where is this host now", so reading the published snapshot each
// time is what keeps a refresh from being invisible for a whole Slot.
type HostBusinessLookup struct {
	store *Store
}

func NewHostBusinessLookup(store *Store) *HostBusinessLookup {
	return &HostBusinessLookup{store: store}
}

// LookupHostBusiness returns the business the host belongs to, and false when
// the index does not hold it.
//
// A store that has never built an index answers false for everything, which is
// the same answer as a host nobody has heard of. That is deliberate and it is
// the safe direction here: not held means not expected, so a cold index expects
// nothing rather than reporting every declared host absent while it warms up.
func (lookup *HostBusinessLookup) LookupHostBusiness(identity string) (string, bool) {
	if lookup == nil || lookup.store == nil {
		return "", false
	}
	facts, found := lookup.store.Current().Lookup(identity)
	if !found || facts == nil {
		return "", false
	}
	return facts.BusinessID, true
}

// HostIndexResolved reports whether there is an index behind those answers.
//
// It exists because the safe direction above is only safe for one host. Asked
// about every host of a target, a store with no index answers "not held" to
// all of them, and a caller that reads that as the target's answer has a
// static target that resolved to nobody -- which is a legitimate state, so
// nothing downstream can tell that this one is not it.
//
// An index holding no hosts is not resolved either, for the reason Health
// already gives it: an empty host cache would put every host-scoped strategy
// out of scope at once, and that is never a real state here. A stale index is
// resolved: it holds hosts and answers about them, and the answer being a few
// minutes old is a lag this deployment tolerates everywhere else, whereas
// calling it unresolved would stop no-data detection on every static target
// for the length of a CMDB hiccup.
func (lookup *HostBusinessLookup) HostIndexResolved() bool {
	if lookup == nil || lookup.store == nil {
		return false
	}
	return lookup.store.HostIndexResolved()
}
