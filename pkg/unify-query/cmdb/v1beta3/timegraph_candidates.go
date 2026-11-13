// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"

// planSharedTopologyCandidates 保留 H 跳可达类型之间的全部诱导边，包括 BFS 树外的边。
// 根的入边也要保留，否则无出边的根实例可能无法被发现。
// 裁剪保持关系顺序，但可能改变节点数字 ID；结果应按身份而非 ID 比较。
func planSharedTopologyCandidates(source cmdb.Resource, hops int, relations []cmdb.Relation) []cmdb.Relation {
	return planSharedTopologyCandidatesFrom([]cmdb.Resource{source}, hops, relations)
}

// planSharedTopologyCandidatesFrom 是多个根类型的裁剪：节点集是各根 H 跳内节点的并集，诱导边是这个
// 节点集内的全部关联，所以按全部根类型可达类型的并集保留两端都在其中的关系。两端分别只在不同根的
// 可达范围内的关系也属于诱导子图，不能按每个根分别裁剪后再取并集。
func planSharedTopologyCandidatesFrom(sources []cmdb.Resource, hops int, relations []cmdb.Relation) []cmdb.Relation {
	reachable := map[cmdb.Resource]bool{}
	roots := make(map[cmdb.Resource]bool, len(sources))
	for _, source := range sources {
		if roots[source] {
			continue
		}
		roots[source] = true
		types := topologyReachableTypes(source, hops, relations)
		if types == nil {
			return relations
		}
		for resource := range types {
			reachable[resource] = true
		}
	}
	result := make([]cmdb.Relation, 0, len(relations))
	for _, relation := range relations {
		if (reachable[relation.V[0]] && reachable[relation.V[1]]) || roots[relation.V[1]] {
			result = append(result, relation)
		}
	}
	return result
}

// 类型可达范围也限定了哪些端点属性可能出现在结果中。
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
