// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package admission

import "strings"

// HostStatusFilter reproduces the third filter of Python's access chain
// (alarm_backends/service/access/data/filters.py: HostStatusFilter): a record
// whose host is in a state the platform marks as not monitored is dropped
// before it can become an alert.
//
// The five branches are transcribed rather than simplified, because each one
// decides real alerts and three of them are asymmetric:
//
//   - a record that names no host at all is kept: the filter is about hosts,
//     and container or custom-report series are not host data;
//   - a record that names a host but carries no usable identity is dropped as
//     invalid, which is the opposite of the branch above;
//   - an address without its cloud is not looked up at all, so such a record
//     is kept even if the same address in cloud 0 is a disabled host;
//   - a host CMDB does not know is dropped;
//   - finally, a known host is dropped when any configured state appears
//     anywhere inside its bk_state - substring, not equality, because the
//     platform's own check is `state in host.bk_state`.
//
// The states are a platform setting an operator can change, so they are given
// to the filter rather than compiled into it, and an empty set means the
// filter must not be installed at all - see NewHostStatusFilter.
type HostStatusFilter struct {
	states []string
}

// NewHostStatusFilter returns a filter for the given disabled states, or false
// when there are none.
//
// The false result is not an error: no configured states means the platform
// disables no host, and installing a filter that can never reject would spend
// a decision per series to always say yes. The caller distinguishes "not
// configured" from "configured empty"; both leave the filter out, and only the
// first is worth reporting.
func NewHostStatusFilter(states []string) (*HostStatusFilter, bool) {
	kept := make([]string, 0, len(states))
	for _, state := range states {
		state = strings.TrimSpace(state)
		if state == "" {
			continue
		}
		kept = append(kept, state)
	}
	if len(kept) == 0 {
		return nil, false
	}
	return &HostStatusFilter{states: kept}, true
}

// States returns the configured states, for the config surface to report what
// the filter is actually deciding on.
func (filter *HostStatusFilter) States() []string {
	if filter == nil {
		return nil
	}
	return append([]string(nil), filter.states...)
}

func (*HostStatusFilter) Name() string { return "host_status" }

func (filter *HostStatusFilter) Admit(_ PlanContext, facts *Facts) Decision {
	if filter == nil || facts == nil {
		return Decision{Admit: true}
	}
	naming := facts.HostNaming
	if !naming.NamedID && !naming.NamedAddress {
		// Not host data. Python returns without touching the record.
		return Decision{Admit: true}
	}
	if !naming.Usable {
		return Decision{Reason: "host_identity_invalid"}
	}
	if !naming.NamedID && !naming.NamedCloud {
		// Python only looks a host up by address when the cloud came with it;
		// otherwise it leaves the record alone rather than guessing an area.
		return Decision{Admit: true}
	}
	if facts.HostFactsUnavailable {
		// The index could not be consulted. Dropping every unresolved host now
		// would turn a cache outage into fleet-wide silence, so the record is
		// kept and the gap is visible in the counter.
		return Decision{Admit: true, Reason: "host_facts_unavailable"}
	}
	if !facts.HostResolved {
		return Decision{Reason: "host_unknown"}
	}
	for _, state := range filter.states {
		if strings.Contains(facts.HostState, state) {
			return Decision{Reason: "monitoring_disabled"}
		}
	}
	return Decision{Admit: true}
}
