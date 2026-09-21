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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

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
	// IDKey is the host id the record supplied, as an index key, and is empty
	// when it supplied none. Python branches on the dimension's value rather
	// than its presence, so a bk_host_id that is there but empty names no id.
	IDKey string
	// AddressKey is the "ip|cloud" key Python's address lookup builds, from
	// bk_target_ip and bk_target_cloud_id. It is kept apart from the host
	// identity attribute because that also carries the ip / bk_cloud_id
	// spellings, which build target-scope keys but which Python never looks a
	// host up by.
	AddressKey string
}

// LookupKey returns the identity Python would look this host up by, and
// whether it would look at all.
//
// The precedence is Python's, and it is not a preference between two answers
// to the same question: an id is used whenever the record carries one, and an
// id CMDB does not know is a host CMDB does not know. Falling back to the
// address there answers a question about one host out of another host's entry,
// and keeps a series Python drops as unknown. The address is used only when
// its cloud came with it, because Python would rather leave the record alone
// than guess an area.
//
// The filter that branches on whether the host was looked up and the fuller
// that performs the lookup both read this one method, so the two cannot drift
// into disagreeing about which host the record is even about.
func (naming HostNaming) LookupKey() (string, bool) {
	if naming.IDKey != "" {
		return naming.IDKey, true
	}
	if naming.NamedAddress && naming.NamedCloud && naming.AddressKey != "" {
		return naming.AddressKey, true
	}
	return "", false
}

// Facts is the enriched identity of one series: what the fullers could work
// out about the thing the series describes.
type Facts struct {
	// Attributes are the candidate values a record can be matched by, keyed
	// by attribute name (contract.AttributeHostIdentity and the others in its
	// table). The target matcher reads the attribute a condition's field
	// names and nothing else, so a fuller that learns a new attribute about
	// the record registers it here and the matcher needs no new case.
	//
	// These are facts about the record, never part of it. They are read by
	// filters and reports; they are not written back into the dimensions,
	// and the alert fingerprint is derived from the dimensions alone. The
	// two must stay apart: a fact from CMDB changes when CMDB changes, and an
	// identity that followed it would split one alert in two on every
	// topology move.
	Attributes map[string][]string
	// Dimensions is the series' own labels as the fullers saw them, held for
	// the conditions that build their candidates from dimension pairs named
	// by the strategy rather than from an attribute filled ahead of time. It
	// is the caller's map, read and never written.
	Dimensions map[string]json.RawMessage
	// HostResolved records whether the host the record names was found in CMDB
	// - the one HostNaming.LookupKey picks, not any identity that happens to
	// resolve. A series whose host is unknown is not the same as one with no
	// host dimensions, and filters need to tell them apart.
	HostResolved bool
	// HostState is the CMDB operational state, for the filter that acts on it.
	HostState string
	// HostBusinessID is the business the resolved host belongs to.
	HostBusinessID string
	// HostAttributes are the scalar fields of the resolved host's cache
	// record, by field name, exposed to the matcher as
	// contract.AttributeHostPrefix + name. The map is the index's own and is
	// read only; keeping a reference costs the series nothing, where copying
	// twenty fields into Attributes would allocate on every series for a
	// target nobody has written yet.
	HostAttributes map[string]string
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
	// FactsUnavailableSource names which index could not be consulted, as
	// the reason the filters report: the host index and the service-instance
	// index are written by different jobs and fail separately, and a counter
	// that folded them would say "CMDB" when only one of the two is missing.
	FactsUnavailableSource string
}

// The indexes enrichment consults, as the reasons a filter reports when one
// of them could not be.
const (
	FactsUnavailableHostIndex            = "host_facts_unavailable"
	FactsUnavailableServiceInstanceIndex = "service_instance_facts_unavailable"
)

// MarkFactsUnavailable records that an index could not be consulted. The
// first index to fail names the reason; a later one does not overwrite it,
// so the report says which dependency went first.
func (facts *Facts) MarkFactsUnavailable(source string) {
	if facts.HostFactsUnavailable {
		return
	}
	facts.HostFactsUnavailable = true
	facts.FactsUnavailableSource = source
}

// FactsUnavailableReason is the bounded reason a filter reports for admitting
// a record it could not decide on.
func (facts *Facts) FactsUnavailableReason() string {
	if facts == nil || !facts.HostFactsUnavailable {
		return ""
	}
	if facts.FactsUnavailableSource == "" {
		return FactsUnavailableHostIndex
	}
	return facts.FactsUnavailableSource
}

// Candidates returns the values the record can be matched by on one
// attribute, or nil when the fullers learned none.
func (facts *Facts) Candidates(attribute string) []string {
	if facts == nil {
		return nil
	}
	if values, found := facts.Attributes[attribute]; found {
		return values
	}
	if name, isHostAttribute := strings.CutPrefix(attribute, contract.AttributeHostPrefix); isHostAttribute {
		if value, found := facts.HostAttributes[name]; found && value != "" {
			return []string{value}
		}
	}
	return nil
}

// Add records one candidate value for an attribute, keeping the values
// unique and in the order they were learned.
func (facts *Facts) Add(attribute string, value string) {
	if attribute == "" || value == "" {
		return
	}
	if facts.Attributes == nil {
		facts.Attributes = make(map[string][]string, 4)
	}
	for _, existing := range facts.Attributes[attribute] {
		if existing == value {
			return
		}
	}
	facts.Attributes[attribute] = append(facts.Attributes[attribute], value)
}

// Set replaces the candidate values of an attribute with a canonical set:
// unique, sorted, empties dropped. An empty set removes the attribute, so a
// fuller that learned nothing leaves no trace that could read as "learned an
// empty list".
func (facts *Facts) Set(attribute string, values []string) {
	if attribute == "" {
		return
	}
	unique := make(map[string]struct{}, len(values))
	canonical := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, seen := unique[value]; seen {
			continue
		}
		unique[value] = struct{}{}
		canonical = append(canonical, value)
	}
	if len(canonical) == 0 {
		if facts.Attributes != nil {
			delete(facts.Attributes, attribute)
		}
		return
	}
	sort.Strings(canonical)
	if facts.Attributes == nil {
		facts.Attributes = make(map[string][]string, 4)
	}
	facts.Attributes[attribute] = canonical
}

// HostKeys are the identities a host record can be matched by: "ip|cloud"
// and the bare host id. Both are kept because a strategy target may name
// either.
func (facts *Facts) HostKeys() []string { return facts.Candidates(contract.AttributeHostIdentity) }

// ServiceInstanceKeys are the service-instance identities of the series.
func (facts *Facts) ServiceInstanceKeys() []string {
	return facts.Candidates(contract.AttributeServiceInstanceID)
}

// TopoNodes are the "obj|inst" nodes the series belongs to. It is a set, not
// a value: a host belongs to every node on every topology link it has, so a
// host in three modules carries three chains of business, set and module,
// and a target naming any one of them includes it.
func (facts *Facts) TopoNodes() []string { return facts.Candidates(contract.AttributeHostTopoNode) }

// AddHostKey records an identity the record can be matched by. Fullers in
// other packages call it, so resolving a host by one identity can teach the
// record the other one.
func (facts *Facts) AddHostKey(key string) { facts.Add(contract.AttributeHostIdentity, key) }

func (facts *Facts) AddServiceInstanceKey(key string) {
	facts.Add(contract.AttributeServiceInstanceID, key)
}

// SetTopoNodes stores the node set canonically so decisions are stable and
// comparable across refreshes.
func (facts *Facts) SetTopoNodes(nodes []string) { facts.Set(contract.AttributeHostTopoNode, nodes) }

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
	// The dimensions are handed to every fuller and kept on the facts by
	// reference. They are read only: a fuller writes what it learns into
	// Attributes, never into this map, so the series the fingerprint is
	// derived from is exactly the series the provider returned.
	facts := Facts{Dimensions: dimensions}
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
	// An admitted decision may still carry a reason, and that is the one worth
	// reporting: it says the filter did not actually decide. Dropping it here
	// is how "the gap is visible in the counter" quietly stops being true -
	// the counter would only ever see an ordinary admission, which is exactly
	// what a filter that has given up looks like from the outside.
	admittedFilter, admittedReason := "", ""
	for _, filter := range chain.filters {
		decision := filter.Admit(plan, facts)
		if !decision.Admit {
			return false, filter.Name(), decision.Reason
		}
		if decision.Reason != "" && admittedReason == "" {
			admittedFilter, admittedReason = filter.Name(), decision.Reason
		}
	}
	return true, admittedFilter, admittedReason
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
