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
func (m *Model) QuerySharedTopology(ctx context.Context, request cmdb.SharedTopologyQuery) (cmdb.SharedTopologyResult, error) {
	return m.querySharedTopology(ctx, request, nil)
}

// QuerySharedTopologySnapshotPlan 在 YOLO 模式下输出自有的快照编码计划。
// 它沿用 snapshots 的请求格式和时间网格校验，供 HTTP 层逐帧写出。
func (m *Model) QuerySharedTopologySnapshotPlan(ctx context.Context, request cmdb.SharedTopologyQuery) (cmdb.SharedTopologySnapshotPlanResult, error) {
	var plan *cmdb.SharedTopologySnapshotPlan
	result, err := m.querySharedTopology(ctx, request, &plan)
	if err != nil {
		return cmdb.SharedTopologySnapshotPlanResult{}, err
	}
	return cmdb.SharedTopologySnapshotPlanResult{StartTime: result.StartTime, EndTime: result.EndTime, Step: result.Step, PointCount: result.PointCount, Plan: plan}, nil
}

func (m *Model) querySharedTopology(ctx context.Context, request cmdb.SharedTopologyQuery, planOut **cmdb.SharedTopologySnapshotPlan) (result cmdb.SharedTopologyResult, err error) {
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
	defer func() {
		if planOut != nil && *planOut != nil {
			p := *planOut
			observeSharedTopologyResult(ctx, queryMode, started, validated, result, err, topologySnapshotCounts{nodes: p.NodeOccurrences, edges: p.EdgeOccurrences, partial: len(p.Partial)})
		} else {
			observeSharedTopologyResult(ctx, queryMode, started, validated, result, err)
		}
	}()
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
	if request.ResponseFormat != "" && request.ResponseFormat != "snapshots" && request.ResponseFormat != cmdb.CompactTopologyFormat {
		return cmdb.SharedTopologyResult{}, fmt.Errorf("unsupported topology response format %q", request.ResponseFormat)
	}
	span.Set("response-format", request.ResponseFormat)
	if planOut != nil && !yoloMode {
		return cmdb.SharedTopologyResult{}, fmt.Errorf("snapshot plan requires YOLO mode")
	}
	if planOut != nil && request.ResponseFormat == cmdb.CompactTopologyFormat {
		return cmdb.SharedTopologyResult{}, fmt.Errorf("snapshot plan requires snapshots format")
	}

	maxHops := request.MaxHops
	if maxHops == 0 {
		maxHops = DefaultMaxHops
	}
	if maxHops < 0 || (!yoloMode && maxHops > MaxAllowedHops) {
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

	attributeRequest := &QueryRequest{SpaceUID: request.SpaceUID, SourceType: FromCMDBResource(request.SourceType), SourceInfo: matcherToMap(request.SourceInfo), SourceExpandInfo: cloneMatcher(request.SourceExpandInfo)}
	if err := validateSourceExpandInfoFields(attributeRequest, provider); err != nil {
		return cmdb.SharedTopologyResult{}, err
	}
	for key, value := range attributeRequest.SourceExpandInfo {
		if identityValue, exists := attributeRequest.SourceInfo[key]; exists && identityValue != value {
			return cmdb.SharedTopologyResult{}, fmt.Errorf("source_expand_info conflicts with source_info for field %q", key)
		}
	}

	for _, key := range provider.GetResourcePrimaryKeys(request.SpaceUID, attributeRequest.SourceType) {
		// 扩展条件里的主键也属于查询身份；非主键只参与根节点的属性筛选。
		if value, exists := attributeRequest.SourceExpandInfo[key]; exists {
			if attributeRequest.SourceInfo == nil {
				attributeRequest.SourceInfo = make(map[string]string)
			}
			attributeRequest.SourceInfo[key] = value
		}
	}
	request.SourceInfo = cmdb.Matcher(attributeRequest.SourceInfo)

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
	allowedRelations := relations
	rootRelations := sharedTopologyRootRelationKeys(request.SourceType, maxHops, relations)
	planSpan.Set("relation-count", len(relations))
	// 候选裁剪只减少取数范围；遍历仍使用完整的关系身份和逐帧时间状态。
	if SharedTopologyPlanCandidates {
		relations = planSharedTopologyCandidates(request.SourceType, maxHops, relations)
	}
	planSpan.Set("candidate-planning-enabled", SharedTopologyPlanCandidates)
	planSpan.Set("planned-relation-count", len(relations))
	planSpan.Set("root-filter-relation-count", len(rootRelations))
	planSpan.Set("max-hops", maxHops)
	planSpan.End(&relationErr)

	queryCtx, cancel := withTimeGraphQueryContext(ctx)
	defer cancel()
	validated = true
	// VM 的取数边界与拓扑评估网格必须一致，避免首末帧被隐式对齐或重复读取。
	queryCtx = metadata.WithExactTimeGrid(queryCtx)
	queryCtx = metadata.WithLeftOpenTimeWindow(queryCtx)
	if SharedTopologyPlanCandidates {
		queryCtx = context.WithValue(queryCtx, timeGraphFetchPlanKey{}, timeGraphFetchPlan{hops: maxHops, allowed: allowedRelations})
	}
	queryCtx, release, err := AcquireSharedTopology(queryCtx)
	if err != nil {
		return cmdb.SharedTopologyResult{}, err
	}
	defer release()
	defer func() {
		_ = ReserveTopologyStage(queryCtx, "matrix", 0)
		_ = ReserveTopologyStage(queryCtx, "graph", 0)
	}()
	if !yoloMode {
		queryCtx = metadata.WithBackendResponseLimit(queryCtx, int64(effectiveTimeGraphLimit(MaxSharedTopologyBackendBytes, 16*1024*1024)))
		span.Set("backend-response-byte-limit", metadata.BackendResponseLimit(queryCtx))
	}

	if planOut != nil && yoloMode {
		queryCtx = context.WithValue(queryCtx, timeGraphDeferredCompressionKey{}, true)
	}

	graph, err := m.buildTimeGraph(
		queryCtx,
		request.SpaceUID,
		start,
		end,
		step,
		request.SourceType,
		request.SourceInfo,
		// 属性筛选只约束根，不改变邻域内其它节点。
		request.SourceExpandInfo,
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
	queryCtx, releaseTraversal, err := graph.withTraversalReservation(queryCtx)
	if err != nil {
		return cmdb.SharedTopologyResult{}, errors.WithMessage(err, "reserve topology traversal")
	}
	defer releaseTraversal()

	topologyQuery := SharedTopologyQuery{
		SourceType:           request.SourceType,
		SourceMatcher:        request.SourceInfo,
		MaxHops:              maxHops,
		AllowedCategories:    toRelationCategories(request.AllowedCategories),
		AllowedRelationTypes: append([]string(nil), request.AllowedRelationTypes...),
		Direction:            direction,
		PartialTimestamps:    graph.partialTimesCopy(),
		RootBits:             graph.sharedTopologyRootBits(),
	}
	if request.ResponseFormat == cmdb.CompactTopologyFormat {
		compact, compactErr := graph.FindCompactTopology(queryCtx, grid, topologyQuery, request.TargetTypes)
		if compactErr != nil {
			return cmdb.SharedTopologyResult{}, errors.WithMessage(compactErr, "export compact topology")
		}
		return cmdb.SharedTopologyResult{
			StartTime:  grid.Timestamps[0],
			EndTime:    grid.Timestamps[len(grid.Timestamps)-1],
			Step:       responseStep,
			PointCount: len(grid.Timestamps),
			Compact:    compact,
		}, nil
	}
	if planOut != nil {
		plan, planErr := graph.findSharedTopologySnapshotPlan(queryCtx, grid, topologyQuery, request.TargetTypes)
		if planErr != nil {
			return cmdb.SharedTopologyResult{}, errors.WithMessage(planErr, "export snapshot plan")
		}
		*planOut = plan
		result = cmdb.SharedTopologyResult{StartTime: grid.Timestamps[0], EndTime: grid.Timestamps[len(grid.Timestamps)-1], Step: responseStep, PointCount: len(grid.Timestamps)}
		span.Set("query-start", result.StartTime)
		span.Set("query-end", result.EndTime)
		span.Set("point-count", result.PointCount)
		span.Set("snapshot-count", len(plan.Timestamps))
		span.Set("snapshot-node-count", plan.NodeOccurrences)
		span.Set("snapshot-edge-count", plan.EdgeOccurrences)
		span.Set("partial-snapshot-count", len(plan.Partial))
		return result, nil
	}
	snapshots, err := graph.findSharedTopologyDTO(queryCtx, grid, topologyQuery)
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
	if err := filterSharedTopologySnapshots(queryCtx, snapshots, request.SourceType, request.TargetTypes); err != nil {
		convertSpan.End(&err)
		metric.CMDBTimeGraphStageObserve(queryCtx, "topology-convert", metric.CMDBTimeGraphErrorResult(err), time.Since(convertStarted))
		return cmdb.SharedTopologyResult{}, errors.WithMessage(err, "filter shared topology")
	}
	result = cmdb.SharedTopologyResult{
		StartTime:  grid.Timestamps[0],
		EndTime:    grid.Timestamps[len(grid.Timestamps)-1],
		Step:       responseStep,
		PointCount: len(grid.Timestamps),
		Snapshots:  snapshots,
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
	if maxPoints > 0 && pointCount > int64(maxPoints) {
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

// topologyRootProbeCoversEndpoints 判断按根身份筛选出边时，是否也能观察到全部入边端点。
// 每条入边必须有读取同一指标、方向相反的对应关系，首跳判空才安全。
func topologyRootProbeCoversEndpoints(source cmdb.Resource, relations []cmdb.Relation) bool {
	for _, incoming := range relations {
		if len(incoming.V) != 2 || incoming.V[1] != source {
			continue
		}
		covered := false
		for _, outgoing := range relations {
			if len(outgoing.V) != 2 || outgoing.V[0] != source || outgoing.V[1] != incoming.V[0] {
				continue
			}
			if outgoing.RelationType == incoming.RelationType && outgoing.MetricName == incoming.MetricName && outgoing.Category == incoming.Category && outgoing.Direction != incoming.Direction {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
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

func filterSharedTopologySnapshots(ctx context.Context, snapshots []cmdb.SharedTopologySnapshot, sourceType cmdb.Resource, targetTypes []cmdb.Resource) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(targetTypes) == 0 {
		return nil
	}
	allowed := make(map[cmdb.Resource]struct{}, len(targetTypes)+1)
	allowed[sourceType] = struct{}{}
	for _, targetType := range targetTypes {
		if err := ctx.Err(); err != nil {
			return err
		}
		allowed[targetType] = struct{}{}
	}
	for index := range snapshots {
		keptNodes := make(map[uint64]struct{})
		nodes := snapshots[index].Nodes[:0]
		for _, node := range snapshots[index].Nodes {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, ok := allowed[node.ResourceType]; !ok {
				continue
			}
			keptNodes[node.ID] = struct{}{}
			nodes = append(nodes, node)
		}
		edges := snapshots[index].Edges[:0]
		for _, edge := range snapshots[index].Edges {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, sourceOK := keptNodes[edge.Source]; !sourceOK {
				continue
			}
			if _, targetOK := keptNodes[edge.Target]; !targetOK {
				continue
			}
			edges = append(edges, edge)
		}
		// 清空 DTO 底层数组中已移除的引用，避免继续持有属性对象。
		clear(snapshots[index].Nodes[len(nodes):])
		clear(snapshots[index].Edges[len(edges):])
		snapshots[index].Nodes = nodes
		snapshots[index].Edges = edges
	}
	return ctx.Err()
}
