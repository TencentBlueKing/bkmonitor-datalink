// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"

// Keep induced edges between types reachable within H hops, including edges
// outside the BFS tree. Incoming root edges are also needed to discover root
// instances when no outgoing edge exists for them.
// Input order is preserved, but removing unrelated nodes can renumber IDs;
// callers must compare topology by identity rather than numeric node ID.
func planSharedTopologyCandidates(source cmdb.Resource, hops int, relations []cmdb.Relation) []cmdb.Relation {
	reachable := topologyReachableTypes(source, hops, relations)
	if reachable == nil {
		return relations
	}
	result := make([]cmdb.Relation, 0, len(relations))
	for _, relation := range relations {
		if (reachable[relation.V[0]] && reachable[relation.V[1]]) || relation.V[1] == source {
			result = append(result, relation)
		}
	}
	return result
}

// Type reachability also bounds which endpoint attributes can be observable.
func topologyReachableTypes(source cmdb.Resource, hops int, relations []cmdb.Relation) map[cmdb.Resource]bool {
	adjacency := make(map[cmdb.Resource][]cmdb.Resource)
	for _, relation := range relations {
		if len(relation.V) != 2 {
			return nil
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
	return reachable
}
