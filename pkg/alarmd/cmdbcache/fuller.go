// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
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
	store *Store
}

func NewHostTopologyFuller(store *Store) *HostTopologyFuller {
	return &HostTopologyFuller{store: store}
}

func (*HostTopologyFuller) Name() string { return "cmdb_host_topology" }

func (fuller *HostTopologyFuller) Fill(_ map[string]json.RawMessage, facts *admission.Facts) {
	if fuller == nil || fuller.store == nil {
		facts.MarkFactsUnavailable(admission.FactsUnavailableHostIndex)
		return
	}
	index := fuller.store.Current()
	if index == nil {
		// Never loaded. A filter that acts on "CMDB does not know this host"
		// has to be able to tell that apart from "CMDB was not asked".
		facts.MarkFactsUnavailable(admission.FactsUnavailableHostIndex)
		return
	}
	if index.Hosts() == 0 {
		// A host cache with nothing in it is not a fleet with no hosts. It is
		// the signature of a cache that was never written, or of a connection
		// pointed somewhere nothing writes it. Deciding on it would put every
		// topology-targeted strategy out of scope and drop every host-named
		// series at once - and silently, because each individual decision
		// looks like an ordinary "this host is unknown". The facts are
		// reported unavailable instead, which is the state the filters already
		// know how to hold: keep the alerts, leave the gap in the counter.
		facts.MarkFactsUnavailable(admission.FactsUnavailableHostIndex)
		return
	}
	hostKeys := facts.HostKeys()
	if len(hostKeys) == 0 {
		return
	}
	// A series names one host, but it may name it by more than one identity
	// (address and host id), and the two are not guaranteed to agree. The node
	// set is the union of everything that resolves, so a host in several
	// modules carries all of its chains.
	nodes := make([]string, 0, 8)
	// Resolution below teaches the record identities it did not arrive with,
	// so the keys to look up are taken before the fuller starts adding any.
	lookups := append([]string(nil), hostKeys...)
	// Two different questions are answered below, and only one of them is
	// about a single host.
	//
	// Whether CMDB knows this host, and what state it is in, is that one: the
	// host Python would have looked up, by id whenever the record carried one
	// and by address only otherwise. Python never falls back from an unknown
	// id to the address, so an id CMDB does not know is a host CMDB does not
	// know. Resolving the address instead would answer "known" out of a
	// different host's entry, and keep a series Python drops.
	resolveHostState(index, facts)
	// Which topology the record sits in, and which identities a target may
	// name it by, is the other question, and there every identity counts: a
	// monitoring target matches on either one. That is TargetCondition's own
	// rule rather than the host status filter's, so the union below is not
	// narrowed to the identity the attributes came from - including when that
	// identity resolved to nothing.
	for _, key := range lookups {
		host, found := index.Lookup(key)
		if !found {
			continue
		}
		nodes = append(nodes, host.TopoNodes...)
		// Resolving by one identity teaches the record its other identity, so
		// a target that names the host the other way still matches.
		if host.HostID != "" {
			facts.AddHostKey(host.HostID)
		}
		if host.IP != "" {
			facts.AddHostKey(host.IP + "|" + host.CloudID)
		}
	}
	if len(nodes) > 0 {
		facts.SetTopoNodes(nodes)
	}
}

// resolveHostState answers whether CMDB knows the host the record names and
// what it says about it, by the one identity Python would look up. It also
// exposes that host's scalar attributes, which the facts serve under
// contract.AttributeHostPrefix, so a target on a host attribute is a table
// row away and needs no fuller change; nothing reads them yet.
func resolveHostState(index *Index, facts *admission.Facts) {
	key, looked := facts.HostNaming.LookupKey()
	if !looked {
		return
	}
	host, found := index.Lookup(key)
	if !found {
		facts.HostResolved, facts.HostState, facts.HostBusinessID, facts.HostAttributes = false, "", "", nil
		return
	}
	facts.HostResolved = true
	facts.HostState, facts.HostBusinessID = host.State, host.BusinessID
	facts.HostAttributes = host.Attributes
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
	store *Store
}

func NewServiceInstanceTopologyFuller(store *Store) *ServiceInstanceTopologyFuller {
	return &ServiceInstanceTopologyFuller{store: store}
}

func (*ServiceInstanceTopologyFuller) Name() string { return "cmdb_service_instance_topology" }

func (fuller *ServiceInstanceTopologyFuller) Fill(_ map[string]json.RawMessage, facts *admission.Facts) {
	instanceKeys := facts.ServiceInstanceKeys()
	if len(instanceKeys) == 0 {
		// Not instance data; nothing here applies.
		return
	}
	if facts.HostNaming.IDKey != "" && facts.HostResolved {
		// Python's host-by-id branch returned before the instance was asked.
		return
	}
	if fuller == nil || fuller.store == nil {
		facts.MarkFactsUnavailable(admission.FactsUnavailableServiceInstanceIndex)
		return
	}
	index := fuller.store.Current()
	if index == nil || index.ServiceInstances() == 0 {
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
