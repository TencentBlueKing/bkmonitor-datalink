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
