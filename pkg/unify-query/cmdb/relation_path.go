// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package cmdb

// RelationPathsFromResourcePaths converts paths that only contain resource
// types into relation paths. Versioned CMDB implementations use this helper
// when the caller does not provide relation identity constraints.
func RelationPathsFromResourcePaths(paths [][]Resource) []RelationPath {
	result := make([]RelationPath, 0, len(paths))
	for _, path := range paths {
		steps := make([]RelationPathStep, 0, len(path))
		for _, resourceType := range path {
			steps = append(steps, RelationPathStep{ResourceType: resourceType})
		}
		result = append(result, RelationPath{Steps: steps})
	}
	return result
}

// ResourcePathsFromRelationPaths drops relation identity constraints while
// preserving the resource sequence in each path.
func ResourcePathsFromRelationPaths(paths []RelationPath) [][]Resource {
	result := make([][]Resource, 0, len(paths))
	for _, path := range paths {
		resources := make([]Resource, 0, len(path.Steps))
		for _, step := range path.Steps {
			resources = append(resources, step.ResourceType)
		}
		result = append(result, resources)
	}
	return result
}

// PathRank returns the position of a concrete path in the candidate list.
// Paths that are not present are ranked after all candidates.
func PathRank(path []PathNode, candidates [][]Resource) int {
	if len(path) == 0 {
		return len(candidates)
	}
	for index, candidate := range candidates {
		if len(candidate) != len(path) {
			continue
		}
		matched := true
		for step, node := range path {
			if node.ResourceType != candidate[step] {
				matched = false
				break
			}
		}
		if matched {
			return index
		}
	}
	return len(candidates)
}

// PathResourceTypes returns the resource sequence in a concrete path.
func PathResourceTypes(path []PathNode) []string {
	result := make([]string, 0, len(path))
	for _, node := range path {
		result = append(result, string(node.ResourceType))
	}
	return result
}
