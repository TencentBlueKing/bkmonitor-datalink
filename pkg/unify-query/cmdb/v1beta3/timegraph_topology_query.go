// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// QuerySharedTopology 查询一个统一时间网格上的完整局部拓扑。
func (m *Model) QuerySharedTopology(ctx context.Context, request cmdb.SharedTopologyQuery) (result cmdb.SharedTopologyResult, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-query-shared-topology")
	defer span.End(&err)
	defer func() { SetTimeGraphLimitTrace(span, err) }()
	queryMode := "instant"
	if request.StartTime != 0 || request.EndTime != 0 {
		queryMode = "range"
	}
	started := time.Now()
	validated := false
	metric.CMDBTopologyInFlightAdd(queryMode, 1)
	defer func() { observeSharedTopologyResult(ctx, queryMode, started, validated, result, err) }()
	span.Set("query-mode", queryMode)
	span.Set("space-uid", request.SpaceUID)
	span.Set("source-type", request.SourceType)
	span.Set("target-types", request.TargetTypes)
	span.Set("source-matcher-count", len(request.SourceInfo))
	span.Set("max-hops", request.MaxHops)
	span.Set("allowed-category-count", len(request.AllowedCategories))
	span.Set("allowed-relation-type-count", len(request.AllowedRelationTypes))
	span.Set("dynamic-relation-direction", request.DynamicRelationDirection)
	span.Set("look-back-delta", request.LookBackDelta)
	if err := ctx.Err(); err != nil {
		return cmdb.SharedTopologyResult{}, err
	}
	if request.SpaceUID == "" {
		return cmdb.SharedTopologyResult{}, errors.New("space uid is empty")
	}
	if request.SourceType == "" {
		return cmdb.SharedTopologyResult{}, errors.New("source type is empty")
	}

	maxHops := request.MaxHops
	if maxHops == 0 {
		maxHops = DefaultMaxHops
	}
	if maxHops < 0 || maxHops > MaxAllowedHops {
		return cmdb.SharedTopologyResult{}, fmt.Errorf("max hops %d is outside [0, %d]", maxHops, MaxAllowedHops)
	}

	direction := TraversalDirection(request.DynamicRelationDirection)
	if direction == "" {
		direction = DirectionBoth
	}
	if direction != DirectionOutbound && direction != DirectionInbound && direction != DirectionBoth {
		return cmdb.SharedTopologyResult{}, fmt.Errorf("unsupported dynamic relation direction %q", direction)
	}

	_, gridSpan := trace.NewSpan(ctx, "timegraph-normalize-topology-grid")
	var gridErr error
	gridSpan.Set("query-mode", queryMode)
	gridSpan.Set("requested-start", request.StartTime)
	gridSpan.Set("requested-end", request.EndTime)
	gridSpan.Set("requested-step", request.Step)
	gridSpan.Set("max-point-count", effectiveMaxSharedTopologyPoints())
	start, end, step, responseStep, grid, gridErr := normalizeSharedTopologyTime(request)
	if gridErr == nil {
		gridSpan.Set("point-count", len(grid.Timestamps))
		gridSpan.Set("normalized-start", grid.Timestamps[0])
		gridSpan.Set("normalized-end", grid.Timestamps[len(grid.Timestamps)-1])
		gridSpan.Set("normalized-step", responseStep)
	}
	gridSpan.End(&gridErr)
	if gridErr != nil {
		return cmdb.SharedTopologyResult{}, gridErr
	}

	_, lookBackSpan := trace.NewSpan(ctx, "timegraph-normalize-lookback")
	var lookBackErr error
	lookBack, lookBackErr := normalizeSharedTopologyLookBack(request.LookBackDelta)
	lookBackSpan.Set("requested-look-back", request.LookBackDelta)
	lookBackSpan.Set("normalized-look-back", lookBack)
	lookBackSpan.End(&lookBackErr)
	if lookBackErr != nil {
		return cmdb.SharedTopologyResult{}, lookBackErr
	}

	provider := m.getSchemaProvider()
	if err := validateSchemaProvider(provider, request.SpaceUID); err != nil {
		return cmdb.SharedTopologyResult{}, errors.WithMessage(err, "validate topology schema")
	}

	_, relationSpan := trace.NewSpan(ctx, "timegraph-resolve-topology-relations")
	var relationErr error
	relations := m.sharedTopologyRelations(request.SpaceUID, request.AllowedCategories, request.AllowedRelationTypes, direction)
	relationSpan.Set("space-uid", request.SpaceUID)
	relationSpan.Set("allowed-category-count", len(request.AllowedCategories))
	relationSpan.Set("allowed-relation-type-count", len(request.AllowedRelationTypes))
	relationSpan.Set("relation-count", len(relations))
	relationSpan.End(&relationErr)
	span.Set("relation-count", len(relations))
	_, planSpan := trace.NewSpan(ctx, "timegraph-plan-topology-fetch")
	rootRelations := sharedTopologyRootRelationKeys(request.SourceType, maxHops, relations)
	planSpan.Set("relation-count", len(relations))
	planSpan.Set("root-filter-relation-count", len(rootRelations))
	planSpan.Set("max-hops", maxHops)
	planSpan.End(&relationErr)

	queryCtx, cancel := context.WithTimeout(ctx, timeGraphQueryTimeout)
	defer cancel()
	validated = true
	queryCtx = withTimeGraphForceSourceInfo(queryCtx)
	queryCtx = metadata.WithExactTimeGrid(queryCtx)
	queryCtx, release, err := AcquireSharedTopology(queryCtx)
	if err != nil {
		return cmdb.SharedTopologyResult{}, err
	}
	defer release()
	queryCtx = metadata.WithBackendResponseLimit(queryCtx, int64(positiveTopologyLimit(MaxSharedTopologyBackendBytes, 16*1024*1024)))
	span.Set("backend-response-byte-limit", metadata.BackendResponseLimit(queryCtx))
	span.Set("matrix-point-limit", positiveTopologyLimit(MaxSharedTopologyMatrixPoints, 1000000))

	graph, err := m.buildTimeGraph(
		queryCtx,
		request.SpaceUID,
		start,
		end,
		step,
		request.SourceType,
		request.SourceInfo,
		// 共享拓扑必须保留没有关系边的种子，因此强制查询源节点信息。
		nil,
		rootRelations,
		relations,
		lookBack,
		nil,
		&grid,
	)
	if err != nil {
		return cmdb.SharedTopologyResult{}, errors.WithMessage(err, "build shared topology")
	}
	defer graph.Clean(queryCtx)

	topologyQuery := SharedTopologyQuery{
		SourceType:           request.SourceType,
		SourceMatcher:        request.SourceInfo,
		MaxHops:              maxHops,
		AllowedCategories:    toRelationCategories(request.AllowedCategories),
		AllowedRelationTypes: append([]string(nil), request.AllowedRelationTypes...),
		Direction:            direction,
		PartialTimestamps:    graph.partialTimesCopy(),
	}
	snapshots, err := graph.FindSharedTopology(queryCtx, grid, topologyQuery)
	if err != nil {
		return cmdb.SharedTopologyResult{}, errors.WithMessage(err, "find shared topology")
	}
	_, convertSpan := trace.NewSpan(queryCtx, "timegraph-convert-topology")
	convertStarted := time.Now()
	inputNodes, inputEdges := 0, 0
	for _, snapshot := range snapshots {
		inputNodes += len(snapshot.Nodes)
		inputEdges += len(snapshot.Edges)
	}
	convertSpan.Set("input-node-count", inputNodes)
	convertSpan.Set("input-edge-count", inputEdges)
	convertSpan.Set("target-type-count", len(request.TargetTypes))
	filterSharedTopologySnapshots(snapshots, request.SourceType, request.TargetTypes)
	result = cmdb.SharedTopologyResult{
		StartTime:  grid.Timestamps[0],
		EndTime:    grid.Timestamps[len(grid.Timestamps)-1],
		Step:       responseStep,
		PointCount: len(grid.Timestamps),
		Snapshots:  convertSharedTopologySnapshots(snapshots),
	}
	convertSpan.Set("snapshot-count", len(result.Snapshots))
	span.Set("query-start", result.StartTime)
	span.Set("query-end", result.EndTime)
	span.Set("point-count", result.PointCount)
	span.Set("snapshot-count", len(result.Snapshots))
	nodeCount, edgeCount, partialCount := 0, 0, 0
	for _, snapshot := range result.Snapshots {
		nodeCount += len(snapshot.Nodes)
		edgeCount += len(snapshot.Edges)
		if snapshot.Partial {
			partialCount++
		}
	}
	convertSpan.Set("output-node-count", nodeCount)
	convertSpan.Set("output-edge-count", edgeCount)
	convertSpan.End(&err)
	metric.CMDBTimeGraphStageObserve(queryCtx, "topology-convert", metric.CMDBRelationResultSuccess, time.Since(convertStarted))
	span.Set("snapshot-node-count", nodeCount)
	span.Set("snapshot-edge-count", edgeCount)
	span.Set("partial-snapshot-count", partialCount)
	return result, nil
}

func normalizeSharedTopologyTime(request cmdb.SharedTopologyQuery) (time.Time, time.Time, time.Duration, string, TopologyGrid, error) {
	if request.StartTime == 0 && request.EndTime == 0 {
		timestamp, err := normalizeTopologyTimestamp(request.Timestamp)
		if err != nil {
			return time.Time{}, time.Time{}, 0, "", TopologyGrid{}, errors.WithMessage(err, "parse timestamp")
		}
		grid, err := NewTopologyGrid([]int64{timestamp})
		if err != nil {
			return time.Time{}, time.Time{}, 0, "", TopologyGrid{}, err
		}
		return time.UnixMilli(timestamp), time.UnixMilli(timestamp), time.Minute, "0s", grid, nil
	}
	if request.StartTime == 0 || request.EndTime == 0 {
		return time.Time{}, time.Time{}, 0, "", TopologyGrid{}, errors.New("start_time and end_time must be provided together")
	}
	startMs, err := normalizeTopologyTimestamp(request.StartTime)
	if err != nil {
		return time.Time{}, time.Time{}, 0, "", TopologyGrid{}, errors.WithMessage(err, "parse start timestamp")
	}
	endMs, err := normalizeTopologyTimestamp(request.EndTime)
	if err != nil {
		return time.Time{}, time.Time{}, 0, "", TopologyGrid{}, errors.WithMessage(err, "parse end timestamp")
	}
	step, err := parseStepDuration(request.Step)
	if err != nil {
		return time.Time{}, time.Time{}, 0, "", TopologyGrid{}, errors.WithMessage(err, "parse step")
	}
	// VM 的 start/end/step 使用整数秒，必须在取模前拒绝无法执行的精度。
	if step < time.Second || step%time.Second != 0 {
		return time.Time{}, time.Time{}, 0, "", TopologyGrid{}, errors.New("topology step must be a positive whole number of seconds")
	}
	stepMs := step.Milliseconds()
	if endMs < startMs {
		return time.Time{}, time.Time{}, 0, "", TopologyGrid{}, errors.New("start_time must be less than or equal to end_time")
	}
	distance := endMs - startMs
	if distance%stepMs != 0 {
		return time.Time{}, time.Time{}, 0, "", TopologyGrid{}, errors.New("range end_time must align with step")
	}
	maxPoints := effectiveMaxSharedTopologyPoints()
	pointCount := distance/stepMs + 1
	if pointCount > int64(maxPoints) {
		return time.Time{}, time.Time{}, 0, "", TopologyGrid{}, &topologyGridLimitError{count: pointCount, limit: maxPoints}
	}
	timestamps := make([]int64, pointCount)
	for index := int64(0); index < pointCount; index++ {
		timestamps[index] = startMs + index*stepMs
	}
	grid, err := NewTopologyGrid(timestamps)
	if err != nil {
		return time.Time{}, time.Time{}, 0, "", TopologyGrid{}, err
	}
	return time.UnixMilli(startMs), time.UnixMilli(endMs), step, step.String(), grid, nil
}

func normalizeTopologyTimestamp(timestamp int64) (int64, error) {
	if timestamp < 0 {
		return 0, errors.New("timestamp must be greater than or equal to 0")
	}
	timestampMs, err := parseTimestamp(strconv.FormatInt(timestamp, 10))
	if err != nil {
		return 0, err
	}
	if timestampMs%1000 != 0 {
		return 0, errors.New("topology timestamp must have whole-second precision")
	}
	return timestampMs, nil
}

// sharedTopologyRootRelationKeys 仅在 H 跳内不会再次到达起点类型时下推种子条件。
// 再次到达的节点也需要出边，包括边界节点之间的诱导边，不能只保留种子的边。
func sharedTopologyRootRelationKeys(source cmdb.Resource, maxHops int, relations []cmdb.Relation) map[timeGraphRelationKey]struct{} {
	result := make(map[timeGraphRelationKey]struct{})
	adjacency := make(map[cmdb.Resource][]cmdb.Resource)
	for _, relation := range relations {
		if len(relation.V) != 2 {
			continue
		}
		adjacency[relation.V[0]] = append(adjacency[relation.V[0]], relation.V[1])
	}
	frontier := []cmdb.Resource{source}
	seen := map[cmdb.Resource]bool{source: true}
	for hop := 0; hop < maxHops && len(frontier) > 0; hop++ {
		next := make([]cmdb.Resource, 0)
		for _, resource := range frontier {
			for _, target := range adjacency[resource] {
				if target == source {
					return result
				}
				if !seen[target] {
					seen[target] = true
					next = append(next, target)
				}
			}
		}
		frontier = next
	}
	for _, relation := range relations {
		if len(relation.V) == 2 && relation.V[0] == source {
			result[timeGraphRelationKeyFor(relation)] = struct{}{}
		}
	}
	return result
}

func normalizeSharedTopologyLookBack(value string) (string, error) {
	if value == "" {
		return fmt.Sprintf("%dms", DefaultLookBackDelta), nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return "", errors.WithMessage(err, "parse look back delta")
	}
	if duration <= 0 {
		return "", errors.New("look back delta must be positive")
	}
	return value, nil
}

func (m *Model) sharedTopologyRelations(namespace string, allowedCategories, allowedRelationTypes []string, direction TraversalDirection) []cmdb.Relation {
	categorySet := make(map[string]struct{}, len(allowedCategories))
	for _, category := range allowedCategories {
		categorySet[category] = struct{}{}
	}
	relationTypeSet := make(map[string]struct{}, len(allowedRelationTypes))
	for _, relationType := range allowedRelationTypes {
		relationTypeSet[relationType] = struct{}{}
	}

	result := make([]cmdb.Relation, 0)
	for _, schema := range m.getSchemaProvider().ListRelationSchemas(namespace) {
		category := string(schema.Category)
		if len(categorySet) > 0 {
			if _, ok := categorySet[category]; !ok {
				continue
			}
		}
		relationType := string(schema.RelationType)
		if len(relationTypeSet) > 0 {
			if _, ok := relationTypeSet[relationType]; !ok {
				continue
			}
		}
		appendRelation := func(source, target ResourceType, edgeDirection TraversalDirection) {
			result = append(result, cmdb.Relation{
				V:            []cmdb.Resource{cmdb.Resource(source), cmdb.Resource(target)},
				RelationType: relationType,
				MetricName:   schema.MetricName,
				Category:     category,
				Direction:    string(edgeDirection),
			})
		}
		if schema.Category == RelationCategoryDynamic {
			if direction == DirectionOutbound || direction == DirectionBoth {
				appendRelation(schema.FromType, schema.ToType, DirectionOutbound)
			}
			if direction == DirectionInbound || direction == DirectionBoth {
				appendRelation(schema.ToType, schema.FromType, DirectionInbound)
			}
			continue
		}
		appendRelation(schema.FromType, schema.ToType, DirectionOutbound)
		if !schema.IsDirectional {
			appendRelation(schema.ToType, schema.FromType, DirectionInbound)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i], result[j]
		if left.RelationType != right.RelationType {
			return left.RelationType < right.RelationType
		}
		if left.V[0] != right.V[0] {
			return left.V[0] < right.V[0]
		}
		if left.V[1] != right.V[1] {
			return left.V[1] < right.V[1]
		}
		return left.Direction < right.Direction
	})
	return result
}

func toRelationCategories(values []string) []RelationCategory {
	result := make([]RelationCategory, len(values))
	for i, value := range values {
		result[i] = RelationCategory(value)
	}
	return result
}

func filterSharedTopologySnapshots(snapshots []SharedTopologySnapshot, sourceType cmdb.Resource, targetTypes []cmdb.Resource) {
	if len(targetTypes) == 0 {
		return
	}
	allowed := make(map[cmdb.Resource]struct{}, len(targetTypes)+1)
	allowed[sourceType] = struct{}{}
	for _, targetType := range targetTypes {
		allowed[targetType] = struct{}{}
	}
	for index := range snapshots {
		keptNodes := make(map[uint64]struct{})
		nodes := snapshots[index].Nodes[:0]
		for _, node := range snapshots[index].Nodes {
			if _, ok := allowed[node.ResourceType]; !ok {
				continue
			}
			keptNodes[node.ID] = struct{}{}
			nodes = append(nodes, node)
		}
		edges := snapshots[index].Edges[:0]
		for _, edge := range snapshots[index].Edges {
			if _, sourceOK := keptNodes[edge.Source]; !sourceOK {
				continue
			}
			if _, targetOK := keptNodes[edge.Target]; !targetOK {
				continue
			}
			edges = append(edges, edge)
		}
		snapshots[index].Nodes = nodes
		snapshots[index].Edges = edges
	}
}

func convertSharedTopologySnapshots(snapshots []SharedTopologySnapshot) []cmdb.SharedTopologySnapshot {
	result := make([]cmdb.SharedTopologySnapshot, len(snapshots))
	for i, snapshot := range snapshots {
		result[i] = cmdb.SharedTopologySnapshot{
			Timestamp:     snapshot.Timestamp,
			Partial:       snapshot.Partial,
			PartialReason: snapshot.PartialReason,
			Nodes:         make([]cmdb.SharedTopologyNode, len(snapshot.Nodes)),
			Edges:         make([]cmdb.SharedTopologyEdge, len(snapshot.Edges)),
		}
		for j, node := range snapshot.Nodes {
			result[i].Nodes[j] = cmdb.SharedTopologyNode{
				ID:           node.ID,
				ResourceType: node.ResourceType,
				Dimensions:   cloneMatcher(node.Dimensions),
			}
		}
		for j, edge := range snapshot.Edges {
			result[i].Edges[j] = cmdb.SharedTopologyEdge{
				Source:       edge.Source,
				Target:       edge.Target,
				RelationType: edge.RelationType,
				MetricName:   edge.MetricName,
				Category:     edge.Category,
				Direction:    edge.Direction,
			}
		}
	}
	return result
}
