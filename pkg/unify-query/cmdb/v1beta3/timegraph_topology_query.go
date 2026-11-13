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

// QuerySharedTopology 查询一个统一时间网格上的完整局部拓扑，snapshots 格式收集全部帧。
func (m *Model) QuerySharedTopology(ctx context.Context, request cmdb.SharedTopologyQuery) (cmdb.SharedTopologyResult, error) {
	var result cmdb.SharedTopologyResult
	err := m.QuerySharedTopologyFrames(ctx, request, func(header cmdb.SharedTopologyResult, frames TopologyFrames) error {
		result = header
		if frames == nil {
			return nil
		}
		result.Snapshots = make([]cmdb.SharedTopologySnapshot, 0, header.PointCount)
		return frames(func(snapshot cmdb.SharedTopologySnapshot) error {
			result.Snapshots = append(result.Snapshots, snapshot)
			return nil
		})
	})
	if err != nil {
		return cmdb.SharedTopologyResult{}, err
	}
	return result, nil
}

// QuerySharedTopologyFrames 与 QuerySharedTopology 给出相同的答案。compact 格式把完整结果交给
// consume，frames 为空；snapshots 格式的 header 不含 Snapshots，frames 在 consume 内逐帧构造快照，
// 调用方可以边构造边编码，不同时物化 K 帧。compact 结果是独立副本，原图在调用 consume 之前释放；
// snapshots 的原图保持到 consume 返回。执行槽都保持到 consume 返回，consume 的错误原样返回。
func (m *Model) QuerySharedTopologyFrames(ctx context.Context, request cmdb.SharedTopologyQuery, consume func(cmdb.SharedTopologyResult, TopologyFrames) error) (err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-query-shared-topology")
	defer span.End(&err)
	defer func() { SetTimeGraphLimitTrace(span, err) }()
	queryMode := "instant"
	if request.StartTime != 0 || request.EndTime != 0 {
		queryMode = "range"
	}
	started := time.Now()
	validated := false
	pointCount := 0
	var counts topologySnapshotCounts
	metric.CMDBTopologyInFlightAdd(queryMode, 1)
	defer func() { observeSharedTopologyResult(ctx, queryMode, started, validated, pointCount, counts, err) }()
	span.Set("query-mode", queryMode)
	span.Set("space-uid", request.SpaceUID)
	span.Set("source-count", len(request.Sources))
	if len(request.Sources) > 0 {
		span.Set("source-type", request.Sources[0].SourceType)
		span.Set("source-matcher-count", len(request.Sources[0].SourceInfo))
	}
	span.Set("target-types", request.TargetTypes)
	span.Set("max-hops", request.MaxHops)
	span.Set("allowed-category-count", len(request.AllowedCategories))
	span.Set("allowed-relation-type-count", len(request.AllowedRelationTypes))
	span.Set("direction", request.Direction)
	span.Set("relation-group-count", len(request.RelationGroups))
	span.Set("look-back-delta", request.LookBackDelta)
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.SpaceUID == "" {
		return errors.New("space uid is empty")
	}
	if len(request.Sources) == 0 {
		return errors.New("topology sources are empty")
	}
	if len(request.Sources) > MaxTopologySources {
		return fmt.Errorf("topology has %d sources: %w", len(request.Sources), &ResultLimitError{Reason: "max_topology_sources", Count: len(request.Sources), Limit: MaxTopologySources})
	}
	for index, source := range request.Sources {
		if source.SourceType == "" {
			if len(request.Sources) == 1 {
				return errors.New("source type is empty")
			}
			return fmt.Errorf("source type is empty in sources[%d]", index)
		}
	}
	if request.ResponseFormat != "" && request.ResponseFormat != "snapshots" && request.ResponseFormat != cmdb.CompactTopologyFormat {
		return fmt.Errorf("unsupported topology response format %q", request.ResponseFormat)
	}
	span.Set("response-format", request.ResponseFormat)

	maxHops := request.MaxHops
	if maxHops == 0 {
		maxHops = DefaultMaxHops
	}
	if maxHops < 0 || maxHops > MaxAllowedHops {
		return fmt.Errorf("max hops %d is outside [0, %d]", maxHops, MaxAllowedHops)
	}

	direction, err := parseTopologyDirection(request.Direction)
	if err != nil {
		return err
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
		gridSpan.Set("point-count", len(grid.timestamps))
		gridSpan.Set("normalized-start", grid.timestamps[0])
		gridSpan.Set("normalized-end", grid.timestamps[len(grid.timestamps)-1])
		gridSpan.Set("normalized-step", responseStep)
	}
	gridSpan.End(&gridErr)
	if gridErr != nil {
		return gridErr
	}

	_, lookBackSpan := trace.NewSpan(ctx, "timegraph-normalize-lookback")
	var lookBackErr error
	lookBack, lookBackErr := normalizeSharedTopologyLookBack(request.LookBackDelta)
	lookBackSpan.Set("requested-look-back", request.LookBackDelta)
	lookBackSpan.Set("normalized-look-back", lookBack)
	lookBackSpan.End(&lookBackErr)
	if lookBackErr != nil {
		return lookBackErr
	}

	provider := m.getSchemaProvider()
	if err := validateSchemaProvider(provider, request.SpaceUID); err != nil {
		return errors.WithMessage(err, "validate topology schema")
	}

	sources, err := normalizeTopologySources(request.SpaceUID, provider, request.Sources)
	if err != nil {
		return err
	}
	primary := sources[0]

	_, relationSpan := trace.NewSpan(ctx, "timegraph-resolve-topology-relations")
	var relationErr error
	relations := m.sharedTopologyRelations(request.SpaceUID, request.AllowedCategories, request.AllowedRelationTypes, request.RelationGroups, direction)
	relationSpan.Set("space-uid", request.SpaceUID)
	relationSpan.Set("allowed-category-count", len(request.AllowedCategories))
	relationSpan.Set("allowed-relation-type-count", len(request.AllowedRelationTypes))
	relationSpan.Set("relation-count", len(relations))
	relationSpan.End(&relationErr)
	span.Set("relation-count", len(relations))
	_, planSpan := trace.NewSpan(ctx, "timegraph-plan-topology-fetch")
	allowedRelations := relations
	// 根条件只在单个选择器时下推；多个选择器由逐跳取数或整个候选分量的读取处理。
	var rootRelations map[timeGraphRelationKey]struct{}
	if len(sources) == 1 {
		rootRelations = sharedTopologyRootRelationKeys(primary.Type, maxHops, relations)
	}
	planSpan.Set("relation-count", len(relations))
	// 候选裁剪只减少取数范围；遍历仍使用完整的关系身份和逐帧时间状态。
	if SharedTopologyPlanCandidates {
		relations = planTopologySourceCandidates(sources, maxHops, relations)
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
		return err
	}
	defer release()
	queryCtx = metadata.WithBackendResponseLimit(queryCtx, timeGraphBackendResponseBytes)
	if len(sources) > 1 {
		queryCtx = context.WithValue(queryCtx, timeGraphTopologySourcesKey{}, sources)
	}
	span.Set("backend-response-byte-limit", metadata.BackendResponseLimit(queryCtx))

	graph, err := m.buildTimeGraph(
		queryCtx,
		request.SpaceUID,
		start,
		end,
		step,
		primary.Type,
		primary.Info,
		// 属性筛选只约束根，不改变邻域内其它节点。
		primary.ExpandInfo,
		rootRelations,
		relations,
		lookBack,
		nil,
		&grid,
	)
	if err != nil {
		return errors.WithMessage(err, "build shared topology")
	}
	if m.timeGraphTopologyBuilt != nil {
		m.timeGraphTopologyBuilt(graph)
	}
	// compact 分支在交给 consume 之前释放原图，其余路径在返回时释放；只释放一次。
	// 清空后不再引用原图：map 清空不缩容，原图不可达后其存储才能回收。
	releaseGraph := func() {
		if graph != nil {
			graph.Clean(queryCtx)
			graph = nil
		}
	}
	defer releaseGraph()

	partialTimestamps := graph.partialTimesCopy()
	// 不完整原因的并集按固定原因计入质量指标，缺路由（relation_no_physical_route）与后端不完整
	// （backend_partial）分开计数；完整原因留在 trace。
	if reasons := topologyPartialReasons(partialTimestamps); reasons != "" {
		span.Set("partial-reasons", reasons)
		metric.CMDBRelationQualityInc(queryCtx, metric.CMDBRelationEntryTopology, queryMode, "partial", reasons)
	}
	topologyQuery := SharedTopologyQuery{
		SourceType:           primary.Type,
		SourceMatcher:        primary.Info,
		MaxHops:              maxHops,
		AllowedCategories:    toRelationCategories(request.AllowedCategories),
		AllowedRelationTypes: append([]string(nil), request.AllowedRelationTypes...),
		Direction:            direction,
		BelongsTo:            m.belongsToRelationTypes(request.SpaceUID),
		PartialTimestamps:    partialTimestamps,
		RootBits:             graph.sharedTopologyRootBits(),
	}
	if len(sources) > 1 {
		topologyQuery.Seeds = graph.topologySourceSeeds(sources)
	}
	header := cmdb.SharedTopologyResult{
		StartTime:  grid.timestamps[0],
		EndTime:    grid.timestamps[len(grid.timestamps)-1],
		Step:       responseStep,
		PointCount: len(grid.timestamps),
	}
	span.Set("query-start", header.StartTime)
	span.Set("query-end", header.EndTime)
	span.Set("point-count", header.PointCount)
	if request.ResponseFormat == cmdb.CompactTopologyFormat {
		compact, compactErr := graph.FindCompactTopology(queryCtx, grid, topologyQuery, request.TargetTypes)
		if compactErr != nil {
			return errors.WithMessage(compactErr, "export compact topology")
		}
		header.Compact = compact
		pointCount, counts = header.PointCount, topologySnapshotCounts{nodes: compact.NodeOccurrences, edges: compact.EdgeOccurrences, partial: len(compact.Partial)}
		releaseGraph()
		return consume(header, nil)
	}
	frames, err := graph.prepareSharedTopologyFrames(queryCtx, grid, topologyQuery, topologyOutputTypes(sources, request.TargetTypes))
	if err != nil {
		return errors.WithMessage(err, "find shared topology")
	}
	// 输出类型过滤在逐帧构造时完成，转换阶段覆盖调用方读取全部帧的过程。
	convertCtx, convertSpan := trace.NewSpan(queryCtx, "timegraph-convert-topology")
	convertStarted := time.Now()
	convertSpan.Set("input-node-count", frames.counts.nodes)
	convertSpan.Set("input-edge-count", frames.counts.edges)
	convertSpan.Set("target-type-count", len(request.TargetTypes))
	var output topologySnapshotCounts
	snapshots := 0
	err = consume(header, func(yield func(cmdb.SharedTopologySnapshot) error) error {
		return frames.each(convertCtx, func(snapshot cmdb.SharedTopologySnapshot) error {
			snapshots++
			output.add(len(snapshot.Nodes), len(snapshot.Edges), snapshot.Partial)
			return yield(snapshot)
		})
	})
	convertSpan.Set("snapshot-count", snapshots)
	convertSpan.Set("output-node-count", output.nodes)
	convertSpan.Set("output-edge-count", output.edges)
	convertSpan.End(&err)
	metric.CMDBTimeGraphStageObserve(queryCtx, "topology-convert", metric.CMDBTimeGraphErrorResult(err), time.Since(convertStarted))
	if err != nil {
		return err
	}
	pointCount, counts = header.PointCount, output
	span.Set("snapshot-count", snapshots)
	span.Set("snapshot-node-count", output.nodes)
	span.Set("snapshot-edge-count", output.edges)
	span.Set("partial-snapshot-count", output.partial)
	return nil
}

// MaxTopologySources 是每个拓扑查询项的选择器上限，是原始输入规格。
const MaxTopologySources = 20

// topologySource 是规范化后的一个根选择器：expand 中的主键并入身份，非主键只筛选该选择器的根。
type topologySource struct {
	Type       cmdb.Resource
	Info       cmdb.Matcher
	ExpandInfo cmdb.Matcher
}

// timeGraphTopologySourcesKey 在多个选择器时把全部选择器交给构图；单个选择器沿用构图参数。
type timeGraphTopologySourcesKey struct{}

// normalizeTopologySources 校验每个选择器的属性字段，并把属性条件里的主键并入身份。
// 只有一个选择器时错误信息与单根请求相同；多个时带上选择器下标。
func normalizeTopologySources(space string, provider SchemaProvider, requested []cmdb.TopologySource) ([]topologySource, error) {
	sources := make([]topologySource, 0, len(requested))
	for index, source := range requested {
		wrap := func(err error) error {
			if len(requested) == 1 || err == nil {
				return err
			}
			return errors.WithMessagef(err, "sources[%d]", index)
		}
		attributeRequest := &QueryRequest{SpaceUID: space, SourceType: FromCMDBResource(source.SourceType), SourceInfo: matcherToMap(source.SourceInfo), SourceExpandInfo: cloneMatcher(source.SourceExpandInfo)}
		if err := validateSourceExpandInfoFields(attributeRequest, provider); err != nil {
			return nil, wrap(err)
		}
		for key, value := range attributeRequest.SourceExpandInfo {
			if identityValue, exists := attributeRequest.SourceInfo[key]; exists && identityValue != value {
				return nil, wrap(fmt.Errorf("source_expand_info conflicts with source_info for field %q", key))
			}
		}
		for _, key := range provider.GetResourcePrimaryKeys(space, attributeRequest.SourceType) {
			// 扩展条件里的主键也属于查询身份；非主键只参与根节点的属性筛选。
			if value, exists := attributeRequest.SourceExpandInfo[key]; exists {
				if attributeRequest.SourceInfo == nil {
					attributeRequest.SourceInfo = make(map[string]string)
				}
				attributeRequest.SourceInfo[key] = value
			}
		}
		sources = append(sources, topologySource{Type: source.SourceType, Info: cmdb.Matcher(attributeRequest.SourceInfo), ExpandInfo: attributeRequest.SourceExpandInfo})
	}
	return sources, nil
}

// planTopologySourceCandidates 取各选择器类型候选关系的并集，保持关系原有顺序。
func planTopologySourceCandidates(sources []topologySource, maxHops int, relations []cmdb.Relation) []cmdb.Relation {
	if len(sources) == 1 {
		return planSharedTopologyCandidates(sources[0].Type, maxHops, relations)
	}
	planned := map[timeGraphRelationKey]bool{}
	seen := map[cmdb.Resource]bool{}
	for _, source := range sources {
		if seen[source.Type] {
			continue
		}
		seen[source.Type] = true
		for _, relation := range planSharedTopologyCandidates(source.Type, maxHops, relations) {
			planned[timeGraphRelationKeyFor(relation)] = true
		}
	}
	result := make([]cmdb.Relation, 0, len(planned))
	for _, relation := range relations {
		if planned[timeGraphRelationKeyFor(relation)] {
			result = append(result, relation)
		}
	}
	return result
}

// topologyOutputTypes 返回 target_types 过滤保留的类型（含全部选择器的源类型）；为空表示保留全部可达类型。
func topologyOutputTypes(sources []topologySource, targetTypes []cmdb.Resource) map[cmdb.Resource]struct{} {
	if len(targetTypes) == 0 {
		return nil
	}
	allowed := make(map[cmdb.Resource]struct{}, len(targetTypes)+len(sources))
	for _, source := range sources {
		allowed[source.Type] = struct{}{}
	}
	for _, targetType := range targetTypes {
		allowed[targetType] = struct{}{}
	}
	return allowed
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

// 关系组由 Schema 推导。
const (
	RelationGroupBelongTo    = "BELONG_TO"
	RelationGroupServiceCall = "SERVICE_CALL"
)

// relationGroupOf 返回关系所属的组：静态且 is_belongs_to 的关系属于 BELONG_TO，动态关系属于 SERVICE_CALL，
// 其余静态关系不属于任何组。
func relationGroupOf(schema RelationSchema) string {
	switch {
	case schema.Category == RelationCategoryDynamic:
		return RelationGroupServiceCall
	case schema.Category == RelationCategoryStatic && schema.IsBelongsTo:
		return RelationGroupBelongTo
	default:
		return ""
	}
}

// sharedTopologyRelations 把 Schema 展开为遍历视图。direction 作用于动态关系（from → to 为 outbound）
// 与归属关系（子 → 父为 outbound）；其余静态关系没有方向，始终两个视图都有。
// allowedCategories、allowedRelationTypes、relationGroups 取交集，空表示不按该项筛选，未知取值不匹配任何关系。
func (m *Model) sharedTopologyRelations(namespace string, allowedCategories, allowedRelationTypes, relationGroups []string, direction TraversalDirection) []cmdb.Relation {
	categorySet := make(map[string]struct{}, len(allowedCategories))
	for _, category := range allowedCategories {
		categorySet[category] = struct{}{}
	}
	relationTypeSet := make(map[string]struct{}, len(allowedRelationTypes))
	for _, relationType := range allowedRelationTypes {
		relationTypeSet[relationType] = struct{}{}
	}
	groupSet := make(map[string]struct{}, len(relationGroups))
	for _, group := range relationGroups {
		groupSet[group] = struct{}{}
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
		group := relationGroupOf(schema)
		if len(groupSet) > 0 {
			if _, ok := groupSet[group]; !ok || group == "" {
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
		if group != "" {
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

// belongsToRelationTypes 返回 namespace 下归属关系的类型名，输出时这些关系每对节点只出一条子 → 父的边。
func (m *Model) belongsToRelationTypes(namespace string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, schema := range m.getSchemaProvider().ListRelationSchemas(namespace) {
		if relationGroupOf(schema) == RelationGroupBelongTo {
			result[string(schema.RelationType)] = struct{}{}
		}
	}
	return result
}

// parseTopologyDirection 把请求的 out/in/both 映射为遍历方向，空值为 both。
func parseTopologyDirection(value string) (TraversalDirection, error) {
	switch value {
	case "", "both":
		return DirectionBoth, nil
	case "out":
		return DirectionOutbound, nil
	case "in":
		return DirectionInbound, nil
	default:
		return "", fmt.Errorf("unsupported topology direction %q, must be out, in or both", value)
	}
}

func toRelationCategories(values []string) []RelationCategory {
	result := make([]RelationCategory, len(values))
	for i, value := range values {
		result[i] = RelationCategory(value)
	}
	return result
}
