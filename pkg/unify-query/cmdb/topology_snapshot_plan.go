// Copyright (C) 2022 THL A29 Limited, a Tencent company.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
package cmdb

import "context"

// SharedTopologySnapshotPlanner 是可选的自有快照编码能力。
// 普通查询仍使用 QuerySharedTopology 及其逐帧预算。
type SharedTopologySnapshotPlanner interface {
	QuerySharedTopologySnapshotPlan(context.Context, SharedTopologyQuery) (SharedTopologySnapshotPlanResult, error)
}
type SharedTopologySnapshotPlanResult struct {
	StartTime, EndTime int64
	Step               string
	PointCount         int
	Plan               *SharedTopologySnapshotPlan
}

// SharedTopologyTimeMask 自有位图字；第 i 位对应第 i 个时间戳。
type (
	SharedTopologyTimeMask struct {
		First uint64
		Rest  []uint64
	}
	SharedTopologySnapshotPlanNode struct {
		Node SharedTopologyNode
		Mask SharedTopologyTimeMask
	}
	SharedTopologySnapshotPlanEdge struct {
		Edge SharedTopologyEdge
		Mask SharedTopologyTimeMask
	}
	SharedTopologySnapshotPlanPartial struct {
		Index  int
		Reason string
	}
	// SharedTopologySnapshotPlan 不逐帧分配完整快照，但保留数字 ID、独立属性、
	// 完整关系身份和时间顺序。同一 ID 的属性版本时间位互不重叠，节点按 ID 排序，
	// 关系沿用快照顺序；图 Clean 后这些字段仍有效。
	SharedTopologySnapshotPlan struct {
		Timestamps                       []int64
		Nodes                            []SharedTopologySnapshotPlanNode
		Edges                            []SharedTopologySnapshotPlanEdge
		Partial                          []SharedTopologySnapshotPlanPartial
		NodeOccurrences, EdgeOccurrences int
	}
)
