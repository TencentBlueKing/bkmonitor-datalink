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
	// (address and host id), and the two are not guaranteed to agree. The node
	// set is the union of everything that resolves, so a host in several
	// modules carries all of its chains.
	nodes := make([]string, 0, 8)
	// Resolution below teaches the record identities it did not arrive with,
	// so the keys to look up are taken before the fuller starts adding any.
	lookups := append([]string(nil), facts.HostKeys...)
	// Two different questions are answered below, and only one of them is
	// about a single host.
	//
	// Whether CMDB knows this host, and what state it is in, is that one: the
	// host Python would have looked up, by id whenever the record carried one
	// and by address only otherwise. Python never falls back from an unknown
	// id to the address, so an id CMDB does not know is a host CMDB does not
	// know. Resolving the address instead would answer "known" out of a
	// different host's entry, and keep a series Python drops.
	if key, looked := facts.HostNaming.LookupKey(); looked {
		if host, found := index.Lookup(key); found {
			facts.HostResolved = true
			facts.HostState, facts.HostBusinessID = host.State, host.BusinessID
		}
	}
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
