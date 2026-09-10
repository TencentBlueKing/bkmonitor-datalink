// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"encoding/json"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
)

// HostTopologyFuller is the CMDB half of enrichment: it turns the host
// identities a series carries into the topology nodes that host belongs to,
// plus the host attributes later filters act on.
//
// It is the counterpart of Python's access fuller, and the reason enrichment
// is a stage rather than part of the filter: the next thing to add - more host
// attributes, service-instance topology, container facts - lands here without
// touching the filters or the call site.
type HostTopologyFuller struct {
	store *Store
}

func NewHostTopologyFuller(store *Store) *HostTopologyFuller {
	return &HostTopologyFuller{store: store}
}

func (*HostTopologyFuller) Name() string { return "cmdb_host_topology" }

func (fuller *HostTopologyFuller) Fill(_ map[string]json.RawMessage, facts *admission.Facts) {
	if fuller == nil || fuller.store == nil {
		facts.HostFactsUnavailable = true
		return
	}
	index := fuller.store.Current()
	if index == nil {
		// Never loaded. A filter that acts on "CMDB does not know this host"
		// has to be able to tell that apart from "CMDB was not asked".
		facts.HostFactsUnavailable = true
		return
	}
	if len(facts.HostKeys) == 0 {
		return
	}
	// A series names one host, but it may name it by more than one identity
	// (address and host id). Any of them resolving is enough; the node set is
	// the union so a host in several modules carries all of its chains.
	nodes := make([]string, 0, 8)
	// Resolution below teaches the record identities it did not arrive with,
	// so the keys to look up are taken before the fuller starts adding any.
	lookups := append([]string(nil), facts.HostKeys...)
	// The attributes a filter acts on come from the host Python would have
	// looked up: by host id whenever the record carried one, by address only
	// otherwise - Python never falls back from an id to an address. Taking
	// whichever identity resolved first instead would, for a record whose id
	// and address name different hosts, read the state of the wrong one, and
	// in one direction that drops a series Python keeps.
	attributed := false
	if key := facts.HostNaming.IDKey; key != "" {
		if host, found := index.Lookup(key); found {
			facts.HostState, facts.HostBusinessID = host.State, host.BusinessID
			attributed = true
		}
	}
	for _, key := range lookups {
		host, found := index.Lookup(key)
		if !found {
			continue
		}
		facts.HostResolved = true
		if !attributed {
			facts.HostState, facts.HostBusinessID = host.State, host.BusinessID
			attributed = true
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
