// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"bytes"
	"encoding/json"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// HostTopologyFuller is the CMDB half of enrichment: it turns the host
// identities a series carries into the topology nodes that host belongs to,
// plus the host attributes later filters act on.
//
// It is the counterpart of Python's access fuller, and the reason enrichment
// is a stage rather than part of the filter: the next thing to add - more host
// attributes, service-instance topology, container facts - lands here without
// touching the filters or the call site.
//
// Everything it learns goes into the facts and nothing into the dimensions.
// Python writes bk_topo_node, bk_target_ip and bk_host_id back into the
// record; this side takes the decisions those writes lead to and not the
// writes, because a dimension that follows CMDB changes the alert's identity
// every time CMDB does.
type HostTopologyFuller struct {
	stores StoreSource
	// acceptStale places on an index past its staleness bound, for a reader
	// that labels rather than decides (attribution); admission does not.
	acceptStale bool
}

// NewHostTopologyFuller places records against the host index of the tenant
// each is read for (Facts.TenantID).
func NewHostTopologyFuller(stores StoreSource) *HostTopologyFuller {
	return &HostTopologyFuller{stores: stores}
}

func (*HostTopologyFuller) Name() string { return "cmdb_host_topology" }

func (fuller *HostTopologyFuller) Fill(dimensions map[string]json.RawMessage, facts *admission.Facts) {
	store := fuller.tenantStore(facts.TenantID)
	if store == nil {
		facts.MarkFactsUnavailable(admission.FactsUnavailableHostIndex)
		return
	}
	index, unusable := store.Usable()
	if unusable != "" && !(fuller.acceptStale && unusable == IndexStale) {
		// Never loaded, past the staleness bound, or empty (Store.judge). A
		// filter that acts on "CMDB does not know this host" has to tell that
		// apart from "CMDB was not asked": past the bound a host added since
		// is unknown and a host moved since sits in its old modules, and an
		// empty cache is a cache nobody writes, not a fleet without hosts -
		// deciding on either would drop host-named series and put targeted
		// ones out of scope one ordinary-looking rejection at a time. The
		// facts are unavailable, by name, which the filters already hold:
		// keep the alerts, leave the gap in the counter (decision-013 section
		// 5.1 item 4).
		facts.MarkFactsUnavailable(admission.FactsUnavailableHostIndex)
		return
	}
	// Which host the record is about is decided the way Python's
	// TopoNodeFuller decides it (fullers.py:55-110): by bk_host_id when the
	// record carries a true one, else by bk_agent_id, else by service
	// instance, else by address,
	// stopping at the first that resolves. Its host status filter and its
	// target match then read the record as the fuller left it, so the host
	// those judge is the one this order picks - and only that host: the
	// identities of a record are not a union of everything that resolves.
	agent := admission.TruthyDimension(dimensions, "bk_agent_id")
	if id := facts.HostNaming.IDKey; id != "" {
		if host, found := index.Lookup(id); found {
			placeByID(facts, id, host)
			facts.PlacedByHostID = true
			facts.ReportedAddressDiffers = reportedAddressDiffers(dimensions, host)
			return
		}
		// An id CMDB does not know is a host CMDB does not know: the status
		// filter looks it up by that id and nothing else, whatever the
		// address below resolves to. Python still takes the topology from
		// the address, and so does this.
	} else if agent != "" {
		// No true id: Python looks the host up by agent next
		// (fullers.py:57-74), and treats it as found by id.
		host, found, unreadable := index.LookupAgent(agent)
		if unreadable {
			facts.MarkFactsUnavailable(admission.FactsUnavailableHostIndex)
			return
		}
		if found {
			placeByAgent(facts, host)
			facts.PlacedByHostID = true
			facts.ReportedAddressDiffers = reportedAddressDiffers(dimensions, host)
			return
		}
	}
	// Past this point a true id the record carried is one CMDB does not know:
	// whatever else places the record's topology, the host the status filter
	// judges is that unknown id.
	unknownID := facts.HostNaming.IDKey != ""
	if instanceResolves(index, facts) {
		// Python's service-instance branch comes before the address one and
		// returns when it resolves; the next fuller is that branch.
		facts.HostUnresolved = unknownID
		return
	}
	address, cloud := admission.FullerAddress(dimensions)
	if address != "" {
		if host, found := index.Lookup(address + "|" + cloud); found {
			placeByAddress(dimensions, facts, cloud, host)
			resolveHostState(index, facts)
			facts.HostUnresolved = unknownID
			return
		}
	}
	resolveHostState(index, facts)
	// Named, and placed by nothing: an id, an address or alias, or an
	// instance the cache did not find.
	facts.HostUnresolved = unknownID || agent != "" || address != "" || len(facts.ServiceInstanceKeys()) > 0
}

// tenantStore is the store of the tenant a record is read for, nil when
// there is none to read.
func (fuller *HostTopologyFuller) tenantStore(tenant string) *Store {
	if fuller == nil || fuller.stores == nil {
		return nil
	}
	return fuller.stores.For(tenant)
}

// placeByAgent is Python's host-by-agent branch, which is its host-by-id
// branch reached another way: the host's address and topology replace the
// record's, and its id is written when the record has no bk_host_id
// dimension at all (fullers.py:66-74).
// reportedAddressDiffers is whether the record's own bk_target_ip is not the
// address of the host CMDB placed it at by id or agent: another one, empty,
// or missing. Python's fuller overwrites the dimension with the host's
// address on that branch (fullers.py:57-74). Exact: the record's string
// against CMDB's, as Python's dedupe would see them.
func reportedAddressDiffers(dimensions map[string]json.RawMessage, host *HostFacts) bool {
	raw, present := dimensions["bk_target_ip"]
	if !present {
		return true
	}
	// A quoted string without an escape is its own value byte for byte:
	// compared as it is, with nothing decoded or allocated on a path every
	// series placed by id takes. Anything else is decoded.
	if n := len(raw); n >= 2 && raw[0] == '"' && raw[n-1] == '"' && bytes.IndexByte(raw, '\\') < 0 {
		return string(raw[1:n-1]) != host.IP
	}
	var reported string
	if err := json.Unmarshal(raw, &reported); err != nil {
		return true
	}
	return reported != host.IP
}

func placeByAgent(facts *admission.Facts, host *HostFacts) {
	naming := &facts.HostNaming
	if !naming.NamedID {
		naming.NamedID, naming.IDKey = true, host.HostID
	}
	keys := make([]string, 0, 2)
	if naming.IDKey != "" {
		keys = append(keys, naming.IDKey)
	}
	if host.IP != "" {
		keys = append(keys, host.IP+"|"+host.CloudID)
	}
	facts.Set(contract.AttributeHostIdentity, keys)
	facts.SetTopoNodes(host.TopoNodes)
	naming.NamedAddress, naming.NamedCloud = true, true
	naming.AddressKey = host.IP + "|" + host.CloudID
	naming.Usable = true
	facts.HostResolved = true
	facts.HostState, facts.HostBusinessID = host.State, host.BusinessID
}

// placeByID is Python's host-by-id branch: the host's own address and cloud
// replace the record's, its topology is the only topology, and nothing else
// is consulted (fullers.py:61-74). The address the record arrived with no
// longer counts for a target - a host that changed its address, or an address
// reused by another host, would otherwise put the record in two hosts' scope.
func placeByID(facts *admission.Facts, id string, host *HostFacts) {
	keys := []string{id}
	if host.IP != "" {
		keys = append(keys, host.IP+"|"+host.CloudID)
	}
	facts.Set(contract.AttributeHostIdentity, keys)
	facts.SetTopoNodes(host.TopoNodes)
	facts.HostNaming.NamedAddress, facts.HostNaming.NamedCloud = true, true
	facts.HostNaming.AddressKey = host.IP + "|" + host.CloudID
	facts.HostResolved = true
	facts.HostState, facts.HostBusinessID = host.State, host.BusinessID
}

// placeByAddress is Python's address branch (fullers.py:92-110): it writes the
// cloud it looked the host up in and the host's topology, and the host's id
// when the record has no bk_host_id dimension at all - one that is there but
// empty is left as it is. bk_target_ip is not written. The status filter then
// finds the host by the written id, which is how a host spelled ip, or given
// without its cloud, is judged by its state.
func placeByAddress(dimensions map[string]json.RawMessage, facts *admission.Facts, cloud string, host *HostFacts) {
	facts.SetTopoNodes(host.TopoNodes)
	naming := &facts.HostNaming
	if !naming.NamedID {
		naming.NamedID, naming.IDKey = true, host.HostID
	}
	naming.NamedCloud = true
	naming.AddressKey = admission.LookupAddressKey(admission.TruthyDimension(dimensions, "bk_target_ip"), cloud)
	naming.Usable = admission.TruthyDimension(dimensions, "bk_target_ip") != "" || naming.IDKey != ""
	keys := make([]string, 0, 2)
	if naming.IDKey != "" {
		keys = append(keys, naming.IDKey)
	}
	if address := admission.TargetAddressKey(dimensions, cloud); address != "" {
		keys = append(keys, address)
	}
	facts.Set(contract.AttributeHostIdentity, keys)
}

// instanceResolves is whether Python's service-instance branch would place the
// record: the instance cache holds instances and one of the record's resolves.
// An empty instance cache resolves nothing there, so Python goes on to the
// address; the instance fuller names that gap separately.
func instanceResolves(index *Index, facts *admission.Facts) bool {
	if index.ServiceInstances() == 0 {
		return false
	}
	for _, key := range facts.ServiceInstanceKeys() {
		if _, found := index.LookupServiceInstance(key); found {
			return true
		}
	}
	return false
}

// resolveHostState answers whether CMDB knows the host the record names and
// what it says about it, by the one identity Python would look up.
func resolveHostState(index *Index, facts *admission.Facts) {
	key, looked := facts.HostNaming.LookupKey()
	if !looked {
		return
	}
	host, found := index.Lookup(key)
	if !found {
		facts.HostResolved, facts.HostState, facts.HostBusinessID = false, "", ""
		return
	}
	facts.HostResolved = true
	facts.HostState, facts.HostBusinessID = host.State, host.BusinessID
}

// ServiceInstanceTopologyFuller resolves a series that names a service
// instance to the instance's module topology and to the host it runs on.
//
// It reproduces the second branch of Python's TopoNodeFuller.full, including
// its precedence: the instance is consulted only when the record did not
// resolve a host by id (Python returns before reaching the instance when a
// bk_host_id lookup succeeds), and when it resolves, the instance's chain
// replaces whatever the record's own address resolved to and the instance's
// host becomes the host the state filter judges - Python overwrites
// bk_target_ip and bk_topo_node, and its host status filter then reads the
// overwritten values. Here those become facts: the topology attribute, the
// host identity attribute and HostNaming change; the dimensions do not.
type ServiceInstanceTopologyFuller struct {
	stores      StoreSource
	acceptStale bool
}

// NewServiceInstanceTopologyFuller places instance records against the
// index of the tenant each is read for.
func NewServiceInstanceTopologyFuller(stores StoreSource) *ServiceInstanceTopologyFuller {
	return &ServiceInstanceTopologyFuller{stores: stores}
}

func (*ServiceInstanceTopologyFuller) Name() string { return "cmdb_service_instance_topology" }

func (fuller *ServiceInstanceTopologyFuller) Fill(_ map[string]json.RawMessage, facts *admission.Facts) {
	instanceKeys := facts.ServiceInstanceKeys()
	if len(instanceKeys) == 0 {
		// Not instance data; nothing here applies.
		return
	}
	if facts.HostResolved {
		// Python's fuller returns once it has placed a host, and the
		// instance is not asked again: by id or by agent (fullers.py:61-74),
		// the agent also when the record's bk_host_id is there but empty; or
		// by address (fullers.py:92-110), which is reached only when no
		// instance resolved - an empty instance cache among those, which
		// would otherwise be marked unavailable here.
		return
	}
	var store *Store
	if fuller != nil && fuller.stores != nil {
		store = fuller.stores.For(facts.TenantID)
	}
	if store == nil {
		facts.MarkFactsUnavailable(admission.FactsUnavailableServiceInstanceIndex)
		return
	}
	index, unusable := store.Usable()
	if (unusable != "" && !(fuller.acceptStale && unusable == IndexStale)) || index.ServiceInstances() == 0 {
		// The same reading as an empty host cache: a series that names an
		// instance while the instance cache holds none is the signature of a
		// cache nobody writes, not of a fleet without instances. Deciding on
		// it would drop every instance-scoped series as unplaceable, one
		// ordinary-looking rejection at a time. The gap is named instead, and
		// separately from the host index, because the two are written by
		// different jobs.
		facts.MarkFactsUnavailable(admission.FactsUnavailableServiceInstanceIndex)
		return
	}
	for _, key := range instanceKeys {
		instance, found := index.LookupServiceInstance(key)
		if !found {
			continue
		}
		// The instance's chain replaces the address-resolved topology rather
		// than joining it: Python assigns bk_topo_node here, and a service
		// target names the instance's module, not every module its host is
		// in.
		facts.SetTopoNodes(instance.TopoNodes)
		// The host identity the record can be matched by is now the
		// instance's host. Python assigns bk_target_ip from the instance, so
		// an address the record arrived with no longer counts; the id the
		// record carried is kept because Python keeps bk_host_id as it was.
		// The instance's own bk_host_id is not added: Python's instance
		// branch never writes it, so is_match sees only the record's id and
		// the instance's address. Adding it would be a superset -- an eq host
		// target would admit more and a neq host target would drop more than
		// Python, and the second is silent.
		hostKeys := make([]string, 0, 2)
		if facts.HostNaming.IDKey != "" {
			hostKeys = append(hostKeys, facts.HostNaming.IDKey)
		}
		if instance.IP != "" {
			hostKeys = append(hostKeys, instance.IP+"|"+instance.CloudID)
		}
		facts.Set(contract.AttributeHostIdentity, hostKeys)
		// Python writes the instance's address and cloud into the record, so
		// its host status filter looks the host up by them - unless the
		// record carried a bk_host_id, which that filter reads first and
		// which stays as it was. HostNaming says the same thing without the
		// write.
		facts.HostNaming.NamedAddress, facts.HostNaming.NamedCloud = true, true
		facts.HostNaming.AddressKey = instance.IP + "|" + instance.CloudID
		facts.HostNaming.Usable = facts.HostNaming.Usable || instance.IP != ""
		resolveHostState(index, facts)
		return
	}
}
