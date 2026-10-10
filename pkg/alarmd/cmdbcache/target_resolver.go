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
	for _, key := range plan.StaticKeys {
		resolution.Static[key] = struct{}{}
	}
	if len(plan.StaticMembers) > 0 {
		resolution.Selectors = append(resolution.Selectors, resolver.resolveStaticMembers(plan))
	}
	for _, id := range plan.DynamicGroups {
		resolution.Selectors = append(resolution.Selectors, resolver.resolveGroup(ctx, plan, id, interval))
	}
	for _, node := range plan.DynamicTopologies {
		resolution.Selectors = append(resolution.Selectors, resolver.resolveTopology(plan, node))
	}
	resolution.Compose()
	return resolution
}

func (resolver *TargetResolver) resolveGroup(ctx context.Context, plan *contract.TargetPlanV1, id string, interval time.Duration) targetplan.SelectorResult {
	result := targetplan.SelectorResult{Kind: targetplan.SelectorKindGroup, ID: id, Reason: targetplan.ReasonNone}
	if resolver == nil || resolver.groups == nil {
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonSourceUnwired
		return result
	}
	lookup := resolver.groups.Group(ctx, id, interval)
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
	}
	if lookup.RefreshFailed {
		result.StaleAge = lookup.Age
	}
	members, dropped := snapshot.Keys(plan)
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
func (resolver *TargetResolver) resolveStaticMembers(plan *contract.TargetPlanV1) targetplan.SelectorResult {
	result := targetplan.SelectorResult{Kind: targetplan.SelectorKindStatic, ID: plan.ModelID, Reason: targetplan.ReasonNone}
	if resolver == nil || resolver.hosts == nil {
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonSourceUnwired
		return result
	}
	index := resolver.hosts.Current()
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

func (resolver *TargetResolver) resolveTopology(plan *contract.TargetPlanV1, node contract.TargetPlanTopologyV1) targetplan.SelectorResult {
	result := targetplan.SelectorResult{Kind: targetplan.SelectorKindTopology, ID: node.Key(), Reason: targetplan.ReasonNone}
	if resolver == nil || resolver.hosts == nil {
		result.State, result.Reason = targetplan.SelectorUnavailable, targetplan.ReasonSourceUnwired
		return result
	}
	index := resolver.hosts.Current()
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
