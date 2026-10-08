// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// TargetResolver resolves a target plan's dynamic references against the
// two caches this package holds: the group store for dynamic groups, the
// host index for dynamic topologies. It reads nothing from Redis on the
// Slot path except a group's first reference; everything else is a lookup
// in memory.
//
// One resolution per Plan per Slot is what the worker asks for, and both
// the admission filter and the no-data round read that one value.
type TargetResolver struct {
	groups *GroupStore
	hosts  *Store
	now    func() time.Time
}

// NewTargetResolver builds the resolver. Either source may be nil: a
// deployment without a group prefix has no group store, and every group
// selector then resolves unavailable by name (source_unwired) rather than
// empty.
func NewTargetResolver(groups *GroupStore, hosts *Store, now func() time.Time) *TargetResolver {
	if now == nil {
		now = time.Now
	}
	return &TargetResolver{groups: groups, hosts: hosts, now: now}
}

// Resolve answers one plan for one Slot. The interval is the Plan's
// evaluation period; a group it references is kept for twice that without
// being asked, so the Plan never reads on its Slot for a group that aged
// out between two of its Slots.
func (resolver *TargetResolver) Resolve(ctx context.Context, plan *contract.TargetPlanV1, interval time.Duration) *targetplan.Resolution {
	if plan == nil {
		return nil
	}
	resolution := &targetplan.Resolution{Static: make(map[string]struct{}, len(plan.StaticKeys))}
	// Pin the host snapshot so included and excluded identities cannot map
	// against different cache revisions within this Slot.
	var index *Index
	var readErr error
	if resolver != nil && resolver.hosts != nil {
		index, readErr = resolver.hosts.targetIndex()
	}
	for _, key := range plan.StaticKeys {
		resolution.Static[key] = struct{}{}
	}
	if len(plan.StaticMembers) > 0 {
		resolution.Selectors = append(resolution.Selectors, resolver.resolveStaticMembers(plan, index))
	}
	if len(plan.StaticHosts) > 0 {
		resolution.Selectors = append(resolution.Selectors, resolver.resolveStaticHosts(plan, index))
	}
	var groupExclusions *excludedGroupFacts
	if plan.Rule == contract.TargetPlanRuleModelInstID && plan.Identity.HostIdentity && len(plan.ExcludeMembers) > 0 {
		groupExclusions = &excludedGroupFacts{hosts: make(map[contract.TargetPlanMemberV1]string, len(plan.ExcludeMembers))}
		for _, member := range plan.ExcludeMembers {
			groupExclusions.hosts[member] = ""
		}
	}
	for _, id := range plan.DynamicGroups {
		resolution.Selectors = append(resolution.Selectors, resolver.resolveGroup(ctx, plan, id, interval, index, groupExclusions))
	}
	for _, node := range plan.DynamicTopologies {
		resolution.Selectors = append(resolution.Selectors, resolver.resolveTopology(plan, node, index))
	}
	if plan.HasExclusions() {
		exclusion := resolver.resolveExclusions(plan, index, readErr, groupExclusions)
		resolution.Excluded = exclusion.Members
		resolution.ExclusionUnavailable = exclusion.State == targetplan.SelectorUnavailable
		// Exclusion evidence is not an inclusion source.
		exclusion.Members = nil
		resolution.Selectors = append(resolution.Selectors, exclusion)
	}
	resolution.Compose()
	return resolution
}

// excludedGroupFacts holds only the plan's excluded canonical members, for
// this Resolve. A group may publish a host before the pinned host index does.
type excludedGroupFacts struct {
	hosts  map[contract.TargetPlanMemberV1]string
	reason string
}

func (facts *excludedGroupFacts) add(plan *contract.TargetPlanV1, lookup GroupLookup, result targetplan.SelectorResult) {
	if facts == nil || facts.reason != "" {
		return
	}
	switch {
	case result.State != targetplan.SelectorOK && result.State != targetplan.SelectorOKEmpty:
		facts.reason = result.Reason
		return
	case lookup.EmptiedHeld:
		facts.reason = targetplan.ReasonEmptiedHeld
		return
	case lookup.RefreshFailed:
		facts.reason = targetplan.ReasonReadFailed
		return
	}
	for _, member := range lookup.Snapshot.Members {
		identity := contract.TargetPlanMemberV1{ModelID: member.ModelID, ModelInstID: member.ModelInstID}
		previous, excluded := facts.hosts[identity]
		if !excluded {
			continue
		}
		// Use only identities accepted by this group's existing Keys checks.
		if _, kept := result.Members[plan.Identity.HostKey(member.HostID)]; !kept {
			continue
		}
		if previous != "" && previous != member.HostID {
			facts.reason = targetplan.ReasonModelUnresolved
			return
		}
		facts.hosts[identity] = member.HostID
	}
}

// resolveExclusions uses frozen keys directly, and checks the same host
// snapshot and validated group facts as inclusion when an identity must be
// mapped. A member absent from both is a normal no-op, not a failure.
func (resolver *TargetResolver) resolveExclusions(plan *contract.TargetPlanV1, index *Index, readErr error, groupFacts *excludedGroupFacts) targetplan.SelectorResult {
	result := targetplan.SelectorResult{Kind: targetplan.SelectorKindExclude, ID: plan.ModelID,
		State: targetplan.SelectorOK, Reason: targetplan.ReasonNone,
		Members: make(map[string]struct{}, len(plan.ExcludeKeys)+len(plan.ExcludeMembers)+len(plan.ExcludeHosts))}
	for _, key := range plan.ExcludeKeys {
		result.Members[key] = struct{}{}
	}
	if len(plan.ExcludeMembers)+len(plan.ExcludeHosts) > 0 {
		switch {
		case resolver == nil || resolver.hosts == nil:
			result.Reason = targetplan.ReasonSourceUnwired
		case readErr != nil:
			result.Reason = targetplan.ReasonReadFailed
		case index == nil || index.Hosts() == 0:
			// An empty or missing host hash has no published completeness
			// marker; neither proves that every excluded host was deleted.
			result.Reason = targetplan.ReasonIndexUnavailable
		case resolver.now().Sub(index.BuiltAt()) > resolver.hosts.maxAge:
			result.Reason = targetplan.ReasonStale
		case !index.SourceRefreshedAt().IsZero() && resolver.now().Sub(index.SourceRefreshedAt()) > resolver.hosts.maxAge:
			result.Reason = targetplan.ReasonStale
		case index.Refused().Hosts > 0 || index.hostIDsIncomplete || len(index.byHostID) != index.Hosts():
			result.Reason = targetplan.ReasonIndexIncomplete
		case len(plan.ExcludeMembers) > 0 && (index.ModelledHosts() != index.Hosts() || len(index.byModelInstance) != index.Hosts()):
			result.Reason = targetplan.ReasonModelUnresolved
		case groupFacts != nil && groupFacts.reason != "":
			result.Reason = groupFacts.reason
		}
		if result.Reason != targetplan.ReasonNone {
			result.State = targetplan.SelectorUnavailable
			return result
		}
	}
	for _, member := range plan.ExcludeMembers {
		host, found := index.LookupModelInstance(member.ModelID, member.ModelInstID)
		hostID := ""
		if found {
			hostID = host.HostID
		}
		if groupFacts != nil {
			if groupHost := groupFacts.hosts[member]; groupHost != "" {
				if hostID != "" && hostID != groupHost {
					result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonModelUnresolved
					result.Dropped++
					result.Kept = len(result.Members)
					return result
				}
				hostID = groupHost
			}
		}
		if hostID == "" {
			// The host enumeration does not enumerate other object models.
			if member.ModelID != contract.HostModelID {
				result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonModelUnresolved
				result.Dropped++
				result.Kept = len(result.Members)
				return result
			}
			result.Dropped++
			continue
		}
		result.Members[plan.Identity.HostKey(hostID)] = struct{}{}
	}
	for _, host := range plan.ExcludeHosts {
		if _, found := index.byHostID[host]; !found {
			result.Dropped++
			continue
		}
		tenant, key, found := index.HostAddress(host)
		if !found || tenant != plan.TenantID {
			result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonAddressUnresolved
			result.Dropped++
			result.Kept = len(result.Members)
			return result
		}
		if _, sharing := index.AddressHost(tenant, key); sharing > 1 {
			result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonAddressAmbiguous
			result.Dropped++
			result.Kept = len(result.Members)
			return result
		}
		result.Members[key] = struct{}{}
	}
	result.Kept = len(result.Members)
	if result.Kept == 0 {
		result.State = targetplan.SelectorOKEmpty
	}
	if result.Dropped > 0 {
		result.Reason = targetplan.ReasonExcludedAbsent
	}
	return result
}

func (resolver *TargetResolver) resolveGroup(ctx context.Context, plan *contract.TargetPlanV1, id string, interval time.Duration, index *Index, exclusions *excludedGroupFacts) targetplan.SelectorResult {
	result := targetplan.SelectorResult{Kind: targetplan.SelectorKindGroup, ID: id, Reason: targetplan.ReasonNone}
	var lookup GroupLookup
	defer func() { exclusions.add(plan, lookup, result) }()
	if resolver == nil || resolver.groups == nil {
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonSourceUnwired
		return result
	}
	lookup = resolver.groups.Group(ctx, id, interval)
	if lookup.ReadErr != nil || lookup.Snapshot == nil {
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonReadFailed
		return result
	}
	snapshot := lookup.Snapshot
	switch {
	case snapshot.Unavailable != "":
		result.State, result.Reason = targetplan.SelectorUnavailable, snapshot.Unavailable
		return result
	case lookup.Age > resolver.groups.MaxAge():
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonStale
		if lookup.EmptiedHeld {
			result.Reason = targetplan.ReasonEmptiedHeld
		}
		return result
	case snapshot.ModelID != plan.ModelID:
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonModelMismatch
		return result
	case plan.Rule == contract.TargetPlanRuleIPCloud && snapshot.TenantID != plan.TenantID:
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonGroupTenantMismatch
		return result
	}
	if lookup.RefreshFailed {
		result.StaleAge = lookup.Age
	}
	members, dropped := snapshot.Keys(plan)
	if plan.Rule == contract.TargetPlanRuleIPCloud {
		// The group's members are hosts by id; their keys are the addresses
		// the host cache has for them now, so a readdressed member moves
		// with the cache and not with the group.
		if !resolver.currentIndex(index) {
			result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonIndexUnavailable
			return result
		}
		hosts := make([]string, 0, len(members))
		for host := range members {
			hosts = append(hosts, host)
		}
		placed, unplaced, ambiguous := placeAddresses(index, plan, hosts)
		return addressSelector(result, placed, snapshot.Dropped+dropped, unplaced, ambiguous)
	}
	result.Members, result.Kept, result.Dropped = members, len(members), snapshot.Dropped+dropped
	switch {
	case result.Dropped > 0:
		// Members were refused, whether or not any were kept: a group whose
		// every member failed validation is not an empty group, and the
		// fact is kept rather than read as "nobody here".
		result.State, result.Reason = targetplan.SelectorIncomplete, targetplan.ReasonMembersDropped
	case len(members) == 0:
		result.State = targetplan.SelectorOKEmpty
	default:
		result.State = targetplan.SelectorOK
	}
	return result
}

// resolveStaticMembers maps the static (model, instance) members of a plan
// read by host identity to host ids through the host cache.
//
// A member the cache knows as a host is that host's id. A member it does
// not know is dropped and counted, as a group member that fails validation
// is. When it knows none of them the selector is Unavailable by name: the
// plan's model is not one the cache lists hosts under - a non-host model the
// writer named no model_match for - or the writer has not put the canonical
// identity on the host records at all (ModelledHosts is zero). Both are
// "these members cannot be placed against the data", and neither is an
// empty target; reading them as one would stop the Plan's detection with
// nothing on the page.
func (resolver *TargetResolver) resolveStaticMembers(plan *contract.TargetPlanV1, index *Index) targetplan.SelectorResult {
	result := targetplan.SelectorResult{Kind: targetplan.SelectorKindStatic, ID: plan.ModelID, Reason: targetplan.ReasonNone}
	if resolver == nil || resolver.hosts == nil {
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonSourceUnwired
		return result
	}
	if index == nil || index.Hosts() == 0 || resolver.now().Sub(index.BuiltAt()) > resolver.hosts.maxAge {
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonIndexUnavailable
		return result
	}
	members := make(map[string]struct{}, len(plan.StaticMembers))
	for _, member := range plan.StaticMembers {
		host, found := index.LookupModelInstance(member.ModelID, member.ModelInstID)
		if !found || host.HostID == "" {
			result.Dropped++
			continue
		}
		members[plan.Identity.HostKey(host.HostID)] = struct{}{}
	}
	result.Members, result.Kept = members, len(members)
	switch {
	case len(members) == 0:
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonModelUnresolved
	case result.Dropped > 0:
		result.State, result.Reason = targetplan.SelectorIncomplete, targetplan.ReasonMembersDropped
	default:
		result.State = targetplan.SelectorOK
	}
	return result
}

// resolveStaticHosts maps the static hosts of an ip_cloud plan, by id, to
// the addresses the host cache has for them inside the plan's tenant.
func (resolver *TargetResolver) resolveStaticHosts(plan *contract.TargetPlanV1, index *Index) targetplan.SelectorResult {
	result := targetplan.SelectorResult{Kind: targetplan.SelectorKindStatic, ID: plan.ModelID, Reason: targetplan.ReasonNone}
	if resolver == nil || resolver.hosts == nil {
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonSourceUnwired
		return result
	}
	if !resolver.currentIndex(index) {
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonIndexUnavailable
		return result
	}
	placed, unplaced, ambiguous := placeAddresses(index, plan, plan.StaticHosts)
	return addressSelector(result, placed, 0, unplaced, ambiguous)
}

// currentIndex reports whether the pinned host index is usable: false when there is
// none, it holds no host, or it is older than the staleness bound.
func (resolver *TargetResolver) currentIndex(index *Index) bool {
	return resolver != nil && resolver.hosts != nil && index != nil && index.Hosts() > 0 &&
		resolver.now().Sub(index.BuiltAt()) <= resolver.hosts.maxAge
}

// placeAddresses maps hosts, by id, to the ip_cloud keys of their target
// addresses inside the plan's tenant. A host with no target address, or
// another tenant's, is unplaced. A host at an address another host of the
// tenant shares is ambiguous: the address names neither, and the host is
// left out rather than matched against both.
func placeAddresses(index *Index, plan *contract.TargetPlanV1, hosts []string) (map[string]struct{}, int, int) {
	placed := make(map[string]struct{}, len(hosts))
	unplaced, ambiguous := 0, 0
	for _, host := range hosts {
		tenant, key, found := index.HostAddress(host)
		if !found || tenant != plan.TenantID {
			unplaced++
			continue
		}
		if _, sharing := index.AddressHost(tenant, key); sharing > 1 {
			ambiguous++
			continue
		}
		placed[key] = struct{}{}
	}
	return placed, unplaced, ambiguous
}

// addressSelector finishes an ip_cloud selector from what it placed. A
// selector none of whose hosts could be placed is Unavailable by name -
// ambiguous when every one shares its address, unresolved otherwise - and
// not an empty target; one that dropped some is Incomplete, naming the
// ambiguity when there was one.
func addressSelector(result targetplan.SelectorResult, placed map[string]struct{}, dropped, unplaced, ambiguous int) targetplan.SelectorResult {
	result.Dropped, result.Kept = dropped+unplaced+ambiguous, len(placed)
	switch {
	case len(placed) == 0 && unplaced == 0 && ambiguous > 0:
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonAddressAmbiguous
		return result
	case len(placed) == 0 && unplaced > 0:
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonAddressUnresolved
		return result
	}
	result.Members = placed
	switch {
	case ambiguous > 0:
		result.State, result.Reason = targetplan.SelectorIncomplete, targetplan.ReasonAddressAmbiguous
	case result.Dropped > 0:
		result.State, result.Reason = targetplan.SelectorIncomplete, targetplan.ReasonMembersDropped
	case len(placed) == 0:
		result.State = targetplan.SelectorOKEmpty
	default:
		result.State = targetplan.SelectorOK
	}
	return result
}

func (resolver *TargetResolver) resolveTopology(plan *contract.TargetPlanV1, node contract.TargetPlanTopologyV1, index *Index) targetplan.SelectorResult {
	result := targetplan.SelectorResult{Kind: targetplan.SelectorKindTopology, ID: node.Key(), Reason: targetplan.ReasonNone}
	if resolver == nil || resolver.hosts == nil {
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonSourceUnwired
		return result
	}
	answer := index.Topology(node.BusinessID, node.ObjectID, node.InstanceID)
	if !answer.Resolved || resolver.now().Sub(index.BuiltAt()) > resolver.hosts.maxAge {
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonIndexUnavailable
		return result
	}
	if !answer.NodeKnown {
		// A node the topology cache does not list: dangling configuration,
		// named as such. Zero members either way; for absence it is a
		// resolved, empty answer, and the name is what tells it apart.
		result.NodeMissing = true
		result.State, result.Reason = targetplan.SelectorOKEmpty, targetplan.ReasonNodeMissing
		return result
	}
	if answer.HostedElsewhere {
		// The node exists and holds hosts, under another business than the
		// reference names: a reference written against the wrong business.
		// Zero members, resolved and empty for absence, named apart from a
		// node that holds no host anywhere.
		result.NodeForeign = true
		result.State, result.Reason = targetplan.SelectorOKEmpty, targetplan.ReasonNodeForeign
		return result
	}
	if plan.Rule == contract.TargetPlanRuleIPCloud {
		hosts := make([]string, 0, len(answer.Hosts))
		dropped := 0
		for _, host := range answer.Hosts {
			if host.HostID == "" {
				dropped++
				continue
			}
			hosts = append(hosts, host.HostID)
		}
		placed, unplaced, ambiguous := placeAddresses(index, plan, hosts)
		return addressSelector(result, placed, dropped, unplaced, ambiguous)
	}
	members := make(map[string]struct{}, len(answer.Hosts))
	for _, host := range answer.Hosts {
		switch plan.Rule {
		case contract.TargetPlanRuleHostID:
			if host.HostID == "" {
				result.Dropped++
				continue
			}
			members[contract.TargetPlanMemberKey(host.HostID)] = struct{}{}
		default:
			if host.ModelID != plan.ModelID || host.ModelInstID == "" {
				// The writer has not put the canonical identity on this host
				// record, or it is another model's: the host cannot be named
				// under this rule.
				result.Dropped++
				continue
			}
			if plan.Identity.HostIdentity {
				// Read by host identity: the host is held under its id, which
				// is what the record's host identity carries.
				if host.HostID == "" {
					result.Dropped++
					continue
				}
				members[plan.Identity.HostKey(host.HostID)] = struct{}{}
				continue
			}
			members[plan.Identity.MemberKey(host.ModelID, host.ModelInstID)] = struct{}{}
		}
	}
	result.Members, result.Kept = members, len(members)
	switch {
	case result.Dropped > 0:
		result.State, result.Reason = targetplan.SelectorIncomplete, targetplan.ReasonMembersDropped
	case len(members) == 0:
		result.State = targetplan.SelectorOKEmpty
	default:
		result.State = targetplan.SelectorOK
	}
	return result
}
