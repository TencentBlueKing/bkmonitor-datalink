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
	stores StoreSource
	// hosts is the store of the tenant whose Plan is being resolved, set on
	// the copy ResolveFor resolves with.
	hosts *Store
	now   func() time.Time
}

// NewTargetResolver builds the resolver. Either source may be nil: a
// deployment without a group prefix has no group store, and every group
// selector then resolves unavailable by name (source_unwired) rather than
// empty.
func NewTargetResolver(groups *GroupStore, hosts StoreSource, now func() time.Time) *TargetResolver {
	if now == nil {
		now = time.Now
	}
	return &TargetResolver{groups: groups, stores: hosts, now: now}
}

// Resolve answers one plan of the default tenant for one Slot (ResolveFor).
func (resolver *TargetResolver) Resolve(ctx context.Context, plan *contract.TargetPlanV1, interval time.Duration) *targetplan.Resolution {
	return resolver.ResolveFor(ctx, "", plan, interval)
}

// ResolveFor answers one plan of a tenant for one Slot, against that
// tenant's host index: the tenant is the Plan's, which a target plan names
// itself only under its ip_cloud rule. The interval is the Plan's
// evaluation period; a group it references is kept for twice that without
// being asked, so the Plan never reads on its Slot for a group that aged
// out between two of its Slots.
func (resolver *TargetResolver) ResolveFor(ctx context.Context, tenant string, plan *contract.TargetPlanV1, interval time.Duration) *targetplan.Resolution {
	if plan == nil {
		return nil
	}
	if resolver != nil && resolver.stores != nil {
		bound := *resolver
		bound.hosts = resolver.stores.For(tenant)
		resolver = &bound
	}
	resolution := &targetplan.Resolution{Static: make(map[string]struct{}, len(plan.StaticKeys))}
	// Pin the host snapshot so included and excluded identities cannot map
	// against different cache revisions within this Slot, with the one
	// judgement on it every selector reads.
	var index *Index
	var unusable string
	var readErr error
	if resolver != nil && resolver.hosts != nil {
		index, unusable, readErr = resolver.hosts.targetIndex()
	}
	for _, key := range plan.StaticKeys {
		resolution.Static[key] = struct{}{}
	}
	if len(plan.StaticMembers) > 0 {
		resolution.Selectors = append(resolution.Selectors, resolver.resolveStaticMembers(plan, index, unusable))
	}
	if len(plan.StaticHosts) > 0 {
		resolution.Selectors = append(resolution.Selectors, resolver.resolveStaticHosts(plan, index, unusable))
	}
	var groupExclusions *excludedGroupFacts
	if plan.Rule == contract.TargetPlanRuleModelInstID && plan.Identity.HostIdentity && len(plan.ExcludeMembers) > 0 {
		groupExclusions = &excludedGroupFacts{hosts: make(map[contract.TargetPlanMemberV1]string, len(plan.ExcludeMembers))}
		for _, member := range plan.ExcludeMembers {
			groupExclusions.hosts[member] = ""
		}
	}
	for _, id := range plan.DynamicGroups {
		resolution.Selectors = append(resolution.Selectors, resolver.resolveGroup(ctx, plan, id, interval, index, unusable, groupExclusions))
	}
	for _, node := range plan.DynamicTopologies {
		resolution.Selectors = append(resolution.Selectors, resolver.resolveTopology(plan, node, index, unusable))
	}
	if plan.HasExclusions() {
		exclusion := resolver.resolveExclusions(plan, index, unusable, groupExclusions)
		resolution.Excluded = exclusion.Members
		resolution.ExclusionUnavailable = exclusion.State == targetplan.SelectorUnavailable
		// Exclusion evidence is not an inclusion source.
		exclusion.Members = nil
		resolution.Selectors = append(resolution.Selectors, exclusion)
	}
	resolver.markServedPastAFailedRefresh(plan, resolution.Selectors, index, readErr)
	resolution.Compose()
	return resolution
}

// markServedPastAFailedRefresh says, on every selector answered from the host
// index, that the index was served past a refresh that failed: the answer
// is the snapshot before, younger than the bound but not the latest the
// writer has (decision-017 section 3.2, resolved_from_stale_snapshot, the
// same rule the group selectors follow). A resolution carrying it is no
// verdict for the target-scope close (decision-024: facts not current are
// not closed on).
//
// The static and topology selectors are answered from the index; so is a
// group under an ip_cloud plan, whose member hosts become addresses through
// it - a member readdressed since is still at its old address there; and so
// is an exclusion that maps members or hosts through it, which would
// subtract a readdressed host at its old address. An exclusion of frozen keys
// alone does not read the index.
func (resolver *TargetResolver) markServedPastAFailedRefresh(plan *contract.TargetPlanV1, selectors []targetplan.SelectorResult, index *Index, readErr error) {
	if readErr == nil || index == nil {
		return
	}
	age, _ := resolver.hosts.factsAge(index)
	if age <= 0 {
		age = time.Nanosecond
	}
	for i := range selectors {
		switch selectors[i].Kind {
		case targetplan.SelectorKindStatic, targetplan.SelectorKindTopology:
		case targetplan.SelectorKindGroup:
			if plan.Rule != contract.TargetPlanRuleIPCloud {
				continue
			}
		case targetplan.SelectorKindExclude:
			if len(plan.ExcludeMembers)+len(plan.ExcludeHosts) == 0 {
				continue
			}
		default:
			continue
		}
		if selectors[i].State == targetplan.SelectorOK || selectors[i].State == targetplan.SelectorOKEmpty {
			if selectors[i].StaleAge < age {
				selectors[i].StaleAge = age
			}
		}
	}
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
//
// An exclusion that maps identities through the host index is unavailable
// only when that index is: never loaded, empty, or past its staleness
// bound - the same index the inclusion side answers from. A snapshot served
// past a failed refresh is within the bound and is answered (the close is
// held by the stale mark instead), and the writer publishes the host hash
// whole, so a snapshot is not judged incomplete record by record.
func (resolver *TargetResolver) resolveExclusions(plan *contract.TargetPlanV1, index *Index, unusable string, groupFacts *excludedGroupFacts) targetplan.SelectorResult {
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
		case unusable != "":
			// An empty or missing host hash proves nothing about whether every
			// excluded host was deleted, and one past its bound may not hold
			// a host added since.
			result.Reason = selectorReason(unusable)
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

func (resolver *TargetResolver) resolveGroup(ctx context.Context, plan *contract.TargetPlanV1, id string, interval time.Duration, index *Index, unusable string, exclusions *excludedGroupFacts) targetplan.SelectorResult {
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
		if unusable != "" {
			result.State, result.Reason = targetplan.SelectorUnavailable, selectorReason(unusable)
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
// is. When it knows none of them, it depends on the model. If the cache
// lists hosts under it, the members are hosts that are gone: Incomplete,
// members_dropped, as a group whose every member was refused - not an empty
// target either. If it lists none - a non-host model the writer named no
// model_match for, or a writer that does not put the canonical identity on
// its host records - the selector is Unavailable by name: these members
// cannot be placed against the data at all.
func (resolver *TargetResolver) resolveStaticMembers(plan *contract.TargetPlanV1, index *Index, unusable string) targetplan.SelectorResult {
	result := targetplan.SelectorResult{Kind: targetplan.SelectorKindStatic, ID: plan.ModelID, Reason: targetplan.ReasonNone}
	if resolver == nil || resolver.hosts == nil {
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonSourceUnwired
		return result
	}
	if unusable != "" {
		result.State, result.Reason = targetplan.SelectorUnavailable, selectorReason(unusable)
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
	case len(members) == 0 && !index.ListsModel(plan.ModelID):
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonModelUnresolved
	case result.Dropped > 0:
		// The cache lists hosts under the plan's model and not these: the
		// members are hosts that are gone, every one of them when none is
		// kept - dropped by name, as a group's refused members are, not a
		// model the cache cannot place.
		result.State, result.Reason = targetplan.SelectorIncomplete, targetplan.ReasonMembersDropped
	default:
		result.State = targetplan.SelectorOK
	}
	return result
}

// resolveStaticHosts maps the static hosts of an ip_cloud plan, by id, to
// the addresses the host cache has for them inside the plan's tenant.
func (resolver *TargetResolver) resolveStaticHosts(plan *contract.TargetPlanV1, index *Index, unusable string) targetplan.SelectorResult {
	result := targetplan.SelectorResult{Kind: targetplan.SelectorKindStatic, ID: plan.ModelID, Reason: targetplan.ReasonNone}
	if resolver == nil || resolver.hosts == nil {
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonSourceUnwired
		return result
	}
	if unusable != "" {
		result.State, result.Reason = targetplan.SelectorUnavailable, selectorReason(unusable)
		return result
	}
	placed, unplaced, ambiguous := placeAddresses(index, plan, plan.StaticHosts)
	return addressSelector(result, placed, 0, unplaced, ambiguous)
}

// selectorReason names, on a selector, why the host index it would read may
// not be decided on (Store.judge): stale past its bound; unavailable never
// loaded or empty.
func selectorReason(unusable string) string {
	if unusable == IndexStale {
		return targetplan.ReasonStale
	}
	return targetplan.ReasonIndexUnavailable
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

func (resolver *TargetResolver) resolveTopology(plan *contract.TargetPlanV1, node contract.TargetPlanTopologyV1, index *Index, unusable string) targetplan.SelectorResult {
	result := targetplan.SelectorResult{Kind: targetplan.SelectorKindTopology, ID: node.Key(), Reason: targetplan.ReasonNone}
	if resolver == nil || resolver.hosts == nil {
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonSourceUnwired
		return result
	}
	if unusable != "" {
		result.State, result.Reason = targetplan.SelectorUnavailable, selectorReason(unusable)
		return result
	}
	answer := index.Topology(node.BusinessID, node.ObjectID, node.InstanceID)
	if !answer.Resolved {
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonIndexUnavailable
		return result
	}
	switch {
	case len(answer.Hosts) > 0:
		// The host cache places hosts under the node: it exists and these
		// are its hosts, whatever the topology cache lists.
	case answer.HostedElsewhere:
	case answer.NodeKnown:
	case index.TopologyNodes() == 0:
		// The topology cache listed no node at all. A missing hash reads as
		// no node, and it is not a published topology (decision-017 section
		// 4, E: a missing hash does not prove an empty set was published),
		// so no node is known to be gone. No host is under this one either:
		// zero members, kept from being a verdict as an incomplete answer.
		result.State, result.Reason = targetplan.SelectorIncomplete, targetplan.ReasonIndexIncomplete
		return result
	default:
		// A node the topology cache does not list and no host sits under:
		// dangling configuration, named as such. Zero members either way;
		// for absence it is a resolved, empty answer, and the name is what
		// tells it apart.
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
