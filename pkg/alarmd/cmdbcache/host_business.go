// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdbcache

import (
	"encoding/json"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
)

// HostBusinessLookup answers which business a host identity belongs to, from
// whichever index snapshot is current when it is asked.
//
// It reads Current() per call rather than holding an Index. A Slot that held
// one would answer from the snapshot it started with, which is the right thing
// for the series inside one round; but this is asked once per Plan and the
// question is "where is this host now", so reading the published snapshot each
// time is what keeps a refresh from being invisible for a whole Slot.
//
// It answers for one tenant, from that tenant's index: the default tenant
// unless ForTenant bound another.
type HostBusinessLookup struct {
	stores StoreSource
	tenant string
}

func NewHostBusinessLookup(stores StoreSource) *HostBusinessLookup {
	return &HostBusinessLookup{stores: stores}
}

// ForTenant is the same lookup answering from the tenant's index.
func (lookup *HostBusinessLookup) ForTenant(tenant string) *HostBusinessLookup {
	if lookup == nil {
		return nil
	}
	return &HostBusinessLookup{stores: lookup.stores, tenant: tenant}
}

// store is the index this lookup answers from, nil when there is none.
func (lookup *HostBusinessLookup) store() *Store {
	if lookup == nil || lookup.stores == nil {
		return nil
	}
	return lookup.stores.For(lookup.tenant)
}

// PlaceHostBusiness answers the business of the host a record is about,
// placed by admission's own fullers over the store: the identity fuller,
// then the host fuller (a true id, the agent, the address) and the
// service-instance fuller, in Python's order (fullers.py:55-110). A record
// they place on no host answers false, and so does one they could not place
// because no index is held or it is empty. An index past its staleness
// bound still places: attribution is a label, not a verdict, and a host's
// business a quarter of an hour on is almost always the same, while the
// fallback - the strategy's own business - is certainly wrong for a host
// target.
func (lookup *HostBusinessLookup) PlaceHostBusiness(dimensions map[string]json.RawMessage) (string, bool) {
	if lookup.store() == nil {
		return "", false
	}
	facts := admission.Facts{Dimensions: dimensions, TenantID: lookup.tenant}
	admission.IdentityFuller{}.Fill(dimensions, &facts)
	(&HostTopologyFuller{stores: lookup.stores, acceptStale: true}).Fill(dimensions, &facts)
	(&ServiceInstanceTopologyFuller{stores: lookup.stores, acceptStale: true}).Fill(dimensions, &facts)
	if !facts.HostResolved || facts.HostFactsUnavailable || facts.HostBusinessID == "" {
		return "", false
	}
	return facts.HostBusinessID, true
}

// LookupHostBusiness returns the business the host belongs to, and false when
// the index does not hold it.
//
// An index that may not be decided on (Store.Usable: never loaded, past its
// bound, or empty) answers false for everything, which is the same answer as
// a host nobody has heard of. That is deliberate and it is the safe direction
// here: not held means not expected, so such an index expects nothing rather
// than reporting every declared host absent. HostIndexResolved tells the two
// apart for a caller asking about a whole target.
func (lookup *HostBusinessLookup) LookupHostBusiness(identity string) (string, bool) {
	store := lookup.store()
	if store == nil {
		return "", false
	}
	index, unusable := store.Usable()
	if unusable != "" {
		return "", false
	}
	facts, found := index.Lookup(identity)
	if !found || facts == nil {
		return "", false
	}
	return facts.BusinessID, true
}

// LookupAddressBusiness returns the business of the one host at an ip_cloud
// address of a tenant, and false when the index holds none there, or more
// than one: an address two hosts share names neither's business.
func (lookup *HostBusinessLookup) LookupAddressBusiness(tenantID, address string) (string, bool) {
	store := lookup.store()
	if store == nil {
		return "", false
	}
	// Attribution's, so a held index past its bound still answers, as
	// PlaceHostBusiness does.
	index, unusable := store.Usable()
	if unusable != "" && unusable != IndexStale {
		return "", false
	}
	host, count := index.AddressHost(tenantID, address)
	if count != 1 || host == nil {
		return "", false
	}
	return host.BusinessID, true
}

// LookupClusterBusiness returns the business the platform published for one
// BCS cluster, from the current snapshot, and false when it published none.
// It is asked when a global business Plan's event on Kubernetes data names
// no business of its own.
func (lookup *HostBusinessLookup) LookupClusterBusiness(clusterID string) (string, bool) {
	store := lookup.store()
	if store == nil {
		return "", false
	}
	return store.Current().LookupClusterBusiness(clusterID)
}

// LookupNamespaceBusiness returns the business the platform published for
// one namespace of one BCS cluster, from the current snapshot, and false
// when it published none.
func (lookup *HostBusinessLookup) LookupNamespaceBusiness(clusterID, namespace string) (string, bool) {
	store := lookup.store()
	if store == nil {
		return "", false
	}
	return store.Current().LookupNamespaceBusiness(clusterID, namespace)
}

// HostIndexResolved reports whether there is an index behind those answers.
//
// It exists because the safe direction above is only safe for one host. Asked
// about every host of a target, a store with no index answers "not held" to
// all of them, and a caller that reads that as the target's answer has a
// static target that resolved to nobody -- which is a legitimate state, so
// nothing downstream can tell that this one is not it.
//
// It is Store.Usable's one judgement: an index holding no hosts is not
// resolved, because an empty host cache would put every host-scoped strategy
// out of scope at once and that is never a real state here; nor is one past
// its staleness bound, whose answers nobody can vouch for any more.
func (lookup *HostBusinessLookup) HostIndexResolved() bool {
	store := lookup.store()
	if store == nil {
		return false
	}
	return store.HostIndexResolved()
}
