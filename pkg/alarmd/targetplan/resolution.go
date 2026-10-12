// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package targetplan

import (
	"sort"
	"time"
)

// SelectorState is what one selector of a target plan came to in one Slot.
// Three of the four are the user's ruling on dynamic targets - a normal
// non-empty answer, a normal empty answer, an unavailable one - and the
// fourth is an answer with members dropped in validation, which is neither
// empty nor whole.
type SelectorState string

const (
	SelectorOK          SelectorState = "OK"
	SelectorOKEmpty     SelectorState = "OKEmpty"
	SelectorIncomplete  SelectorState = "Incomplete"
	SelectorUnavailable SelectorState = "Unavailable"
)

// SelectorStates is the closed list, for the metric.
var SelectorStates = []SelectorState{SelectorOK, SelectorOKEmpty, SelectorIncomplete, SelectorUnavailable}

// The selector kinds.
const (
	SelectorKindStatic   = "static"
	SelectorKindGroup    = "dynamic_group"
	SelectorKindTopology = "dynamic_topology"
	SelectorKindExclude  = "exclude"
)

// The reasons a selector is unavailable or incomplete, closed. They are
// the words of the ruling's table and the metric's reason label.
const (
	ReasonNone             = "none"
	ReasonKeyMissing       = "key_missing"
	ReasonJSONInvalid      = "json_invalid"
	ReasonStructureInvalid = "structure_invalid"
	ReasonModelMismatch    = "model_mismatch"
	ReasonReadFailed       = "read_failed"
	ReasonStale            = "stale"
	ReasonIndexUnavailable = "index_unavailable"
	ReasonIndexIncomplete  = "index_incomplete"
	// ReasonExcludedAbsent counts exclusions absent from a complete host
	// snapshot. The selector stays OK or OKEmpty and the plan remains usable.
	ReasonExcludedAbsent = "excluded_absent"
	ReasonNodeMissing    = "node_missing"
	ReasonNodeForeign    = "node_in_other_business"
	ReasonMembersDropped = "members_dropped"
	ReasonSourceUnwired  = "source_unwired"
	// ReasonModelUnresolved is a static selector of a model_inst_id plan read
	// by host identity whose members the host cache knows no host for: a
	// model that is not the host model, or a host cache the writer has not
	// put the canonical (model, instance) identity on. Either way the members
	// cannot be placed against the data and are named rather than read as
	// an empty target. This is the only word for it: the compiler freezes
	// such a plan as read by host identity and refuses nothing, so a
	// catalog disposition never carries the model question.
	ReasonModelUnresolved = "model_representation_unresolved"
	// ReasonGroupTenantMismatch is a dynamic group an ip_cloud plan references
	// that is not the plan's tenant's, or names no tenant: its members would
	// be read as addresses inside the wrong tenant.
	ReasonGroupTenantMismatch = "tenant_mismatch"
	// ReasonAddressUnresolved is an ip_cloud selector none of whose hosts
	// has a target address inside the plan's tenant: the host cache carries
	// none for them yet - its writer puts one on a host only once it has
	// refreshed with the source's whole address - or they are another
	// tenant's. Not an empty target.
	ReasonAddressUnresolved = "address_unresolved"
	// ReasonAddressAmbiguous is an ip_cloud member at an address another
	// host of the tenant shares: the address names neither host, and the
	// member is left out rather than matched against both.
	ReasonAddressAmbiguous = "address_ambiguous"
)

// SelectorReasons is the closed list, for the metric.
var SelectorReasons = []string{
	ReasonNone, ReasonKeyMissing, ReasonJSONInvalid, ReasonStructureInvalid, ReasonModelMismatch, ReasonReadFailed,
	ReasonStale, ReasonIndexUnavailable, ReasonNodeMissing, ReasonNodeForeign, ReasonMembersDropped, ReasonSourceUnwired,
	ReasonModelUnresolved, ReasonGroupTenantMismatch, ReasonAddressUnresolved, ReasonAddressAmbiguous,
	ReasonIndexIncomplete, ReasonExcludedAbsent,
}

// SelectorResult is one selector's answer: its members in the plan's key
// form, how it came to them, and what it dropped on the way.
type SelectorResult struct {
	Kind  string
	ID    string
	State SelectorState
	// Reason is the closed word behind an Unavailable or Incomplete state,
	// "excluded_absent" for normal missing exclusions, and "none" otherwise.
	Reason string
	// Members are keys, in the plan's identity form, shared with the source
	// snapshot and never written.
	Members map[string]struct{}
	// Dropped and Kept count the members validation refused and accepted.
	Dropped, Kept int
	// StaleAge is non-zero when the answer came from a snapshot kept past a
	// failed refresh: the reader is eating old grain and is told so.
	StaleAge time.Duration
	// NodeMissing is a topology reference to a node the topology cache does
	// not list: a dangling configuration, reported beside the empty answer.
	NodeMissing bool
	// NodeForeign is a topology reference to a node that holds hosts only
	// under another business than the reference names: a reference written
	// against the wrong business, reported beside the empty answer.
	NodeForeign bool
}

// ResolutionState is the whole target plan's state, composed from its
// selectors: any Unavailable makes it Unavailable, else any Incomplete
// makes it Incomplete, else Complete.
type ResolutionState string

const (
	ResolutionComplete    ResolutionState = "Complete"
	ResolutionIncomplete  ResolutionState = "Incomplete"
	ResolutionUnavailable ResolutionState = "Unavailable"
)

// ResolutionStates is the closed list, for the metric.
var ResolutionStates = []ResolutionState{ResolutionComplete, ResolutionIncomplete, ResolutionUnavailable}

// Failure is one named selector failure, for the facts and the object
// page: which selector, what happened.
type Failure struct {
	Kind   string
	ID     string
	Reason string
	// Dropped and Kept are filled for a members-dropped failure.
	Dropped, Kept int
}

// Resolution is a target plan resolved for one Slot: the static keys, one
// result per selector, the composed state, and the failures by name. The
// admission filter asks Contains; the no-data round reads State and
// Members; both read the same value, which is the point.
type Resolution struct {
	Static map[string]struct{}
	// Excluded uses the same keys as Static and selector members. When its
	// identity cannot be resolved, neither admission nor absence may use
	// the included set, because doing so would widen the configured target.
	Excluded             map[string]struct{}
	ExclusionUnavailable bool
	Selectors            []SelectorResult
	State                ResolutionState
	Failures             []Failure
	// NodesMissing lists topology references whose node the cache does not
	// list; NodesForeign those whose node holds hosts under another
	// business only. Both reach the object row's target_resolutions; how
	// the page names them is the page's.
	NodesMissing []string
	NodesForeign []string
	// StaleAge is the largest StaleAge among the selectors, for the facts.
	StaleAge time.Duration
}

// Contains answers the admission filter from the included sources minus
// exclusions. Failed inclusion selectors add no members; an unresolved
// exclusion makes the whole target unavailable and admits nothing.
func (resolution *Resolution) Contains(key string) bool {
	if resolution == nil || resolution.ExclusionUnavailable {
		return false
	}
	if _, excluded := resolution.Excluded[key]; excluded {
		return false
	}
	if _, found := resolution.Static[key]; found {
		return true
	}
	for index := range resolution.Selectors {
		if _, found := resolution.Selectors[index].Members[key]; found {
			return true
		}
	}
	return false
}

// Members is the sorted union minus exclusions, shared by admission and
// the no-data roster. Only a Complete resolution can judge absence; an
// unresolved exclusion returns no members to either consumer.
func (resolution *Resolution) Members() []string {
	if resolution == nil || resolution.ExclusionUnavailable {
		return nil
	}
	union := make(map[string]struct{}, len(resolution.Static))
	for key := range resolution.Static {
		union[key] = struct{}{}
	}
	for index := range resolution.Selectors {
		for key := range resolution.Selectors[index].Members {
			union[key] = struct{}{}
		}
	}
	members := make([]string, 0, len(union))
	for key := range union {
		if _, excluded := resolution.Excluded[key]; !excluded {
			members = append(members, key)
		}
	}
	sort.Strings(members)
	return members
}

// Compose fills State, Failures, NodesMissing and StaleAge from the
// selectors. It is the one place the composition rule lives.
func (resolution *Resolution) Compose() {
	resolution.State = ResolutionComplete
	resolution.Failures = resolution.Failures[:0]
	resolution.NodesMissing = resolution.NodesMissing[:0]
	resolution.NodesForeign = resolution.NodesForeign[:0]
	resolution.StaleAge = 0
	for _, selector := range resolution.Selectors {
		switch selector.State {
		case SelectorUnavailable:
			resolution.State = ResolutionUnavailable
			resolution.Failures = append(resolution.Failures, Failure{Kind: selector.Kind, ID: selector.ID, Reason: selector.Reason, Dropped: selector.Dropped, Kept: selector.Kept})
		case SelectorIncomplete:
			if resolution.State != ResolutionUnavailable {
				resolution.State = ResolutionIncomplete
			}
			// Members were dropped; the selector says why when it knows more
			// than that (an address two hosts share).
			reason := selector.Reason
			if reason == "" || reason == ReasonNone {
				reason = ReasonMembersDropped
			}
			resolution.Failures = append(resolution.Failures, Failure{Kind: selector.Kind, ID: selector.ID, Reason: reason, Dropped: selector.Dropped, Kept: selector.Kept})
		}
		if selector.NodeMissing {
			resolution.NodesMissing = append(resolution.NodesMissing, selector.ID)
		}
		if selector.NodeForeign {
			resolution.NodesForeign = append(resolution.NodesForeign, selector.ID)
		}
		if selector.StaleAge > resolution.StaleAge {
			resolution.StaleAge = selector.StaleAge
		}
	}
}
