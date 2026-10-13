// Copyright (C) 2022 THL A29 Limited, a Tencent company.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
package cmdb

import "context"

// SharedTopologySnapshotPlanner is an optional owned snapshot export capability.
// Ordinary queries retain QuerySharedTopology and its per-frame budgets.
type SharedTopologySnapshotPlanner interface {
	QuerySharedTopologySnapshotPlan(context.Context, SharedTopologyQuery) (SharedTopologySnapshotPlanResult, error)
}
type SharedTopologySnapshotPlanResult struct {
	StartTime, EndTime int64
	Step               string
	PointCount         int
	Plan               *SharedTopologySnapshotPlan
}

// SharedTopologyTimeMask owns its words; bit i denotes timestamp i.
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
	// SharedTopologySnapshotPlan preserves numeric IDs, independent attribute maps,
	// full edge identities and chronological frames without allocating each frame.
	// Nodes are ordered by numeric ID, with disjoint versions of the same ID.
	// Edges use the snapshot edge order. All fields remain valid after graph Clean.
	SharedTopologySnapshotPlan struct {
		Timestamps                       []int64
		Nodes                            []SharedTopologySnapshotPlanNode
		Edges                            []SharedTopologySnapshotPlanEdge
		Partial                          []SharedTopologySnapshotPlanPartial
		NodeOccurrences, EdgeOccurrences int
	}
)
