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
// to the filter rather than compiled into it. Only the last branch reads them:
// with none configured the filter still drops invalid and unknown hosts, as
// Python's does - it is always installed (processor.py:76-80).
type HostStatusFilter struct {
	states []string
}

// NewHostStatusFilter returns the filter for the given disabled states. An
// empty list disables no host by state; the filter still decides the other
// four branches.
//
// Each state is trimmed and an empty one is left out, which Python does not
// do: its `state in host.bk_state` with an empty state is true for every
// host, so one empty entry in the platform's list would drop every host
// series there. This is a rule difference kept on purpose, awaiting product
// (the target-scope review of 2026-10-09, section 6).
func NewHostStatusFilter(states []string) *HostStatusFilter {
	kept := make([]string, 0, len(states))
	for _, state := range states {
		state = strings.TrimSpace(state)
		if state == "" {
			continue
		}
		kept = append(kept, state)
	}
	return &HostStatusFilter{states: kept}
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
	if _, looked := naming.LookupKey(); !looked {
		// Python only looks a host up by address when the cloud came with it;
		// otherwise it leaves the record alone rather than guessing an area.
		// The same method picks the lookup the enrichment performed, so this
		// branch and the host the attributes came from cannot disagree.
		return Decision{Admit: true}
	}
	if facts.HostFactsUnavailable {
		// The index could not be consulted. Dropping every unresolved host now
		// would turn a cache outage into fleet-wide silence, so the record is
		// kept and the gap is visible in the counter.
		return Decision{Admit: true, Reason: facts.FactsUnavailableReason()}
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
