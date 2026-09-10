// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package admission decides whether one series may be evaluated for one plan.
//
// It is shaped as two chains because the thing it reproduces has two: Python's
// access path first enriches a record with facts derived from CMDB (its
// fullers), then runs a chain of filters over the enriched record. Keeping the
// same split means a later fuller - more host attributes, service-instance
// topology, container facts - is a new implementation on the chain rather than
// a change at the call site, and the same holds for a new filter such as the
// host operational-state one.
package admission

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// Facts is the enriched identity of one series: what the fullers could work
// out about the thing the series describes.
//
// TopoNodes is a set, not a value. A host belongs to every node on every
// topology link it has, so a host in three modules carries three chains of
// business, set and module, and a target naming any one of them includes it.
// HostNaming is what a series' own dimensions said about its host.
type HostNaming struct {
	// NamedID is true when a bk_host_id dimension is present, whatever value.
	NamedID bool
	// NamedAddress is true when an address dimension is present.
	NamedAddress bool
	// NamedCloud is true when a cloud dimension is present.
	NamedCloud bool
	// Usable is true when an identity could actually be built from them.
	Usable bool
	// IDKey is the host id the record supplied, as an index key. It is kept
	// because Python looks a host up by its id whenever the record carries
	// one and never falls back to the address, so the attributes a filter
	// acts on have to come from that host and not from whichever identity
	// happened to resolve first.
	IDKey string
}

type Facts struct {
	// HostKeys are the identities a host record can be matched by: "ip|cloud"
	// and the bare host id. Both are kept because a strategy target may name
	// either.
	HostKeys []string
	// ServiceInstanceKeys are the service-instance identities of the series.
	ServiceInstanceKeys []string
	// TopoNodes are the "obj|inst" nodes the series belongs to.
	TopoNodes []string
	// HostResolved records whether a host identity was found in CMDB. A series
	// whose host is unknown is not the same as one with no host dimensions,
	// and filters need to tell them apart.
	HostResolved bool
	// HostState is the CMDB operational state, for the filter that acts on it.
	HostState string
	// HostBusinessID is the business the resolved host belongs to.
	HostBusinessID string
	// HostNaming records what the series said about its host, rather than what
	// could be made of it. The host status filter needs the difference: Python
	// keeps a record that names no host at all, drops one that names a host it
	// cannot use, and looks a host up only when the record gives an id, or an
	// address together with its cloud.
	HostNaming HostNaming
	// HostFactsUnavailable says the CMDB facts could not be consulted at all,
	// as opposed to being consulted and finding nothing. A filter that drops
	// unresolved hosts must not do so while the index is missing: that would
	// turn a cache outage into fleet-wide silence.
	HostFactsUnavailable bool
}

// AddHostKey records an identity the record can be matched by. Fullers in
// other packages call it, so resolving a host by one identity can teach the
// record the other one.
func (facts *Facts) AddHostKey(key string) {
	if key == "" {
		return
	}
	for _, existing := range facts.HostKeys {
		if existing == key {
			return
		}
	}
	facts.HostKeys = append(facts.HostKeys, key)
}

func (facts *Facts) AddServiceInstanceKey(key string) {
	if key == "" {
		return
	}
	for _, existing := range facts.ServiceInstanceKeys {
		if existing == key {
			return
		}
	}
	facts.ServiceInstanceKeys = append(facts.ServiceInstanceKeys, key)
}

// SetTopoNodes stores the node set canonically so decisions are stable and
// comparable across refreshes.
func (facts *Facts) SetTopoNodes(nodes []string) {
	unique := make(map[string]struct{}, len(nodes))
	canonical := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node == "" {
			continue
		}
		if _, seen := unique[node]; seen {
			continue
		}
		unique[node] = struct{}{}
		canonical = append(canonical, node)
	}
	sort.Strings(canonical)
	facts.TopoNodes = canonical
}

// PlanContext is what a filter may know about the plan it is deciding for.
// New filters read new fields here; the chain itself stays unchanged.
type PlanContext struct {
	TenantID   string
	BusinessID string
	StrategyID string
	// TargetScope is the strategy's monitoring target, frozen at compile time.
	// Nil means the strategy names no target.
	TargetScope *TargetScope
}

// Fuller derives facts from a series' dimensions.
type Fuller interface {
	Name() string
	Fill(dimensions map[string]json.RawMessage, facts *Facts)
}

// Decision is a filter's answer. Reason is recorded on rejection so a dropped
// series can be explained without re-deriving why.
type Decision struct {
	Admit  bool
	Reason string
}

// Filter decides whether an enriched series may be evaluated for a plan.
type Filter interface {
	Name() string
	Admit(plan PlanContext, facts *Facts) Decision
}

// Chain runs the fullers once per series, then the filters once per plan.
// The split matters: enrichment is per series and shared across every plan the
// series feeds, while admission is per plan because each strategy has its own
// target.
type Chain struct {
	fullers []Fuller
	filters []Filter
}

func NewChain(fullers []Fuller, filters []Filter) *Chain {
	return &Chain{fullers: append([]Fuller(nil), fullers...), filters: append([]Filter(nil), filters...)}
}

// Enrich derives the facts of one series once.
func (chain *Chain) Enrich(dimensions map[string]json.RawMessage) Facts {
	facts := Facts{}
	if chain == nil {
		return facts
	}
	for _, fuller := range chain.fullers {
		fuller.Fill(dimensions, &facts)
	}
	return facts
}

// Admit runs the filters in order and stops at the first rejection, returning
// which filter rejected and why.
func (chain *Chain) Admit(plan PlanContext, facts *Facts) (bool, string, string) {
	if chain == nil {
		return true, "", ""
	}
	for _, filter := range chain.filters {
		decision := filter.Admit(plan, facts)
		if !decision.Admit {
			return false, filter.Name(), decision.Reason
		}
	}
	return true, "", ""
}

// FilterNames reports the chain's composition, for the resolved-configuration
// record: a filter that is not installed must be visible as absent rather than
// inferred from behaviour.
func (chain *Chain) FilterNames() []string {
	if chain == nil {
		return nil
	}
	names := make([]string, 0, len(chain.filters))
	for _, filter := range chain.filters {
		names = append(names, filter.Name())
	}
	return names
}

func (chain *Chain) FullerNames() []string {
	if chain == nil {
		return nil
	}
	names := make([]string, 0, len(chain.fullers))
	for _, fuller := range chain.fullers {
		names = append(names, fuller.Name())
	}
	return names
}

// dimensionText reads a dimension value as text. Dimensions arrive as raw JSON
// and the same field is a string in one result table and a number in another,
// so both are accepted and normalised to the text form CMDB keys use.
func dimensionText(dimensions map[string]json.RawMessage, name string) string {
	raw, found := dimensions[name]
	if !found || len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err == nil {
		value := strings.TrimSpace(number.String())
		if parsed, err := strconv.ParseFloat(value, 64); err == nil && parsed == float64(int64(parsed)) {
			return strconv.FormatInt(int64(parsed), 10)
		}
		return value
	}
	return ""
}
