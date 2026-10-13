// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.
package v1beta3

import (
	"slices"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

type (
	timeGraphFetchPlanKey struct{}
	timeGraphFetchPlan    struct {
		hops    int
		allowed []cmdb.Relation
	}
)

func topologyRelationComponent(source cmdb.Resource, relations []cmdb.Relation) []cmdb.Relation {
	seen := map[cmdb.Resource]bool{source: true}
	queue := []cmdb.Resource{source}
	for len(queue) > 0 {
		resource := queue[0]
		queue = queue[1:]
		for _, r := range relations {
			if len(r.V) != 2 {
				continue
			}
			for i, endpoint := range r.V {
				if endpoint == resource && !seen[r.V[1-i]] {
					seen[r.V[1-i]] = true
					queue = append(queue, r.V[1-i])
				}
			}
		}
	}
	result := []cmdb.Relation{}
	for _, r := range relations {
		if len(r.V) == 2 && seen[r.V[0]] {
			result = append(result, r)
		}
	}
	return result
}

func topologyStaticBidirectional(relations []cmdb.Relation) bool {
	for _, r := range relations {
		if len(r.V) != 2 || r.Category != string(RelationCategoryStatic) {
			return false
		}
		reversed := false
		for _, other := range relations {
			if len(other.V) == 2 && r.V[0] == other.V[1] && r.V[1] == other.V[0] &&
				r.MetricName == other.MetricName && r.RelationType == other.RelationType &&
				r.Category == other.Category &&
				((r.Direction == string(DirectionOutbound) && other.Direction == string(DirectionInbound)) ||
					(r.Direction == string(DirectionInbound) && other.Direction == string(DirectionOutbound))) {
				reversed = true
				break
			}
		}
		if !reversed {
			return false
		}
	}
	return true
}

// A non-primary attribute projected as another endpoint's primary field can be
// merged from graph-external series by bulk reads. Narrowing would change that
// presentation even if identities and edges were unchanged.
func (q *TimeGraph) topologyProjectionCompatible(relations []cmdb.Relation) bool {
	for _, r := range relations {
		if len(r.V) != 2 {
			return false
		}
		sourcePrefix, targetPrefix := q.relationEndpointPrefixes(r, r.V[0], r.V[1])
		dynamic := r.Category == string(RelationCategoryDynamic)
		// These are exactly the dimensions emitted by MakeQueryTsWithWindow.
		fields, _ := q.relationQueryFields(nil, r.V[0], r.V[1], sourcePrefix, targetPrefix, dynamic)
		projected := make(map[string]bool, len(fields))
		for _, field := range fields {
			projected[field] = true
		}
		for i, endpoint := range r.V {
			primary := q.nodeBuilder.resourceIndexes(endpoint)
			if len(primary) == 0 {
				return false
			}
			prefix := sourcePrefix
			if i == 1 {
				prefix = targetPrefix
			}
			for _, field := range q.nodeBuilder.resourceInfoFields(endpoint) {
				if !slices.Contains(primary, field) && (projected[field] || (dynamic && projected[prefix+field])) {
					return false
				}
			}
		}
	}
	return true
}

func (q *TimeGraph) topologyFetchMode(source cmdb.Resource, identity cmdb.Matcher, planned []cmdb.Relation, plan timeGraphFetchPlan) (string, []cmdb.Relation) {
	component := topologyRelationComponent(source, plan.allowed)
	if !q.completeFrontierIdentity(source, identity) || !topologyStaticBidirectional(component) ||
		!q.topologyProjectionCompatible(planned) {
		return "bulk", nil
	}
	if plan.hops == 1 {
		neighbors := map[cmdb.Resource]bool{}
		for _, r := range component {
			if r.V[0] == source {
				neighbors[r.V[1]] = true
			}
		}
		if neighbors[source] {
			return "bulk", component
		}
		for _, r := range component {
			if neighbors[r.V[0]] && neighbors[r.V[1]] {
				return "bulk", component
			}
		}
		return "star", component
	}
	if plan.hops == 2 {
		colors := map[cmdb.Resource]bool{source: false}
		queue := []cmdb.Resource{source}
		for len(queue) > 0 {
			resource := queue[0]
			queue = queue[1:]
			for _, r := range component {
				if r.V[0] != resource {
					continue
				}
				color, exists := colors[r.V[1]]
				if exists {
					if color == colors[resource] {
						return "bulk", component
					}
				} else {
					colors[r.V[1]] = !colors[resource]
					queue = append(queue, r.V[1])
				}
			}
		}
		return "bipartite", component
	}
	return "bulk", component
}

// Compute which types can lose a root field within H. A shared bare primary
// label on both static endpoints preserves equality; crossing a missing field
// loses it permanently. One still-bound endpoint suffices to constrain an
// induced edge. Other relations remain wide, including cross-scope boundaries.
func (q *TimeGraph) topologyRangeFilters(source cmdb.Resource, identity cmdb.Matcher, component, planned []cmdb.Relation, hops int) map[timeGraphRelationKey]cmdb.Matcher {
	filters := map[timeGraphRelationKey]cmdb.Matcher{}
	if len(component) == 0 || hops < 1 || !q.topologyProjectionCompatible(planned) {
		return filters
	}
	type state struct {
		resource cmdb.Resource
		bound    bool
	}
	type visit struct {
		state
		depth int
	}
	for _, field := range q.nodeBuilder.resourceIndexes(source) {
		value := identity[field]
		if value == "" {
			continue
		}
		first := state{source, true}
		seen := map[state]bool{first: true}
		unknown := map[cmdb.Resource]bool{}
		queue := []visit{{first, 0}}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			if current.depth >= hops {
				continue
			}
			for _, r := range component {
				if r.V[0] != current.resource {
					continue
				}
				preserves := slices.Contains(q.nodeBuilder.resourceIndexes(r.V[0]), field) &&
					slices.Contains(q.nodeBuilder.resourceIndexes(r.V[1]), field)
				next := state{r.V[1], current.bound && preserves}
				if !next.bound {
					unknown[next.resource] = true
				}
				if !seen[next] {
					seen[next] = true
					queue = append(queue, visit{next, current.depth + 1})
				}
			}
		}
		for _, r := range planned {
			if len(r.V) != 2 || r.Category != string(RelationCategoryStatic) {
				continue
			}
			safe := false
			for _, endpoint := range r.V {
				if seen[state{endpoint, true}] && !unknown[endpoint] && slices.Contains(q.nodeBuilder.resourceIndexes(endpoint), field) {
					safe = true
				}
			}
			if safe {
				key := timeGraphRelationKeyFor(r)
				if filters[key] == nil {
					filters[key] = cmdb.Matcher{}
				}
				filters[key][field] = value
			}
		}
	}
	return filters
}
