// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"

// Keep every incident relation, not just BFS tree edges or edges within the
// reachable types. Boundary relations can contribute seed/attribute versions.
// Input order is preserved, but removing unrelated nodes can renumber IDs;
// this planner is therefore opt-in and does not promise byte-identical IDs.
func planSharedTopologyCandidates(source cmdb.Resource, hops int, relations []cmdb.Relation) []cmdb.Relation {
	adjacency := make(map[cmdb.Resource][]cmdb.Resource)
	for _, relation := range relations {
		if len(relation.V) != 2 {
			return relations
		}
		adjacency[relation.V[0]] = append(adjacency[relation.V[0]], relation.V[1])
	}
	reachable := map[cmdb.Resource]bool{source: true}
	frontier := []cmdb.Resource{source}
	for hop := 0; hop < hops && len(frontier) > 0; hop++ {
		next := make([]cmdb.Resource, 0)
		for _, from := range frontier {
			for _, to := range adjacency[from] {
				if !reachable[to] {
					reachable[to] = true
					next = append(next, to)
				}
			}
		}
		frontier = next
	}
	result := make([]cmdb.Relation, 0, len(relations))
	for _, relation := range relations {
		if reachable[relation.V[0]] || reachable[relation.V[1]] {
			result = append(result, relation)
		}
	}
	return result
}
