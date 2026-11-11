// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"maps"
	"slices"
	"sort"

	"github.com/pkg/errors"
	pl "github.com/prometheus/prometheus/promql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// errTopologyHopIncompleteIdentity 表示到达的节点缺少完整主键，无法写成身份条件。
var errTopologyHopIncompleteIdentity = errors.New("reached topology node has incomplete primary fields")

// topologyHopFetchEligible 判断逐跳取数能否给出与整条读取关系分量相同的答案。
// 投影只含可见端点的主键时，节点属性与读到哪些边无关；根条件只含源类型主键时，
// 按条件读取根的关联关系即读入每个可能成为根的节点的全部关联边。
func (q *TimeGraph) topologyHopFetchEligible(source cmdb.Resource, identity cmdb.Matcher, relations []cmdb.Relation, visible map[cmdb.Resource]bool) bool {
	if len(identity) == 0 || len(relations) == 0 || !q.topologyProjectionCompatible(relations, visible) {
		return false
	}
	primary := q.nodeBuilder.resourceIndexes(source)
	for field := range identity {
		if !slices.Contains(primary, field) {
			return false
		}
	}
	return true
}

// topologyReverseRelation 判断 other 是否读取与 relation 相同的物理序列、只交换两端。
func topologyReverseRelation(relation, other cmdb.Relation) bool {
	return len(relation.V) == 2 && len(other.V) == 2 && relation.V[0] == other.V[1] && relation.V[1] == other.V[0] &&
		relation.MetricName == other.MetricName && relation.RelationType == other.RelationType && relation.Category == other.Category &&
		((relation.Direction == string(DirectionOutbound) && other.Direction == string(DirectionInbound)) ||
			(relation.Direction == string(DirectionInbound) && other.Direction == string(DirectionOutbound)))
}

// topologyAnchorFilter 把源类型主键上的根条件写成关系锚点一端的查询标签。
func (q *TimeGraph) topologyAnchorFilter(relation cmdb.Relation, targetAnchor bool, identity cmdb.Matcher) cmdb.Matcher {
	_, fields := q.frontierAnchorFields(relation, targetAnchor)
	filter := make(cmdb.Matcher, len(identity))
	for label, field := range fields {
		if value, ok := identity[field]; ok {
			filter[label] = value
		}
	}
	return filter
}

// topologyHopFetch 记录逐跳取数已读入的范围。图中只会多出整条读取本来也有的边，
// 答案由共享图的逐帧时间位决定；这里只需保证三类边全部读入：
//   - 根条件命中的节点的全部关联边（决定根及其时间位）；
//   - H 跳以内每层前沿节点的全部出边（决定逐层可达）；
//   - 两端都已到达的边（诱导边）。
type topologyHopFetch struct {
	loader   *timeGraphMatrixLoader
	space    string
	source   cmdb.Resource
	identity cmdb.Matcher
	// relations 是有物理路由的候选关系，保持规划顺序。
	relations []cmdb.Relation
	// reverse 是与关系共用物理序列的反向关系；一次读取同时写入两个方向。
	reverse map[timeGraphRelationKey]cmdb.Relation
	// whole 是已整条读入的关系，之后不再按身份读取。
	whole map[timeGraphRelationKey]bool
	// expanded 是全部出边（及成对反向关系的入边）已读入的节点。
	expanded map[uint64]bool
	// roots 缓存源类型节点是否命中根条件。
	roots   map[uint64]bool
	queries *int
	reads   int
}

// loadTopologyHops 从根逐跳读取实际到达的候选：首跳按根条件读取根的全部关联关系，
// 之后每跳只读取上一跳到达节点的出边，H 跳后补读两端都已到达、尚未读入的诱导边。
// 取数选择器使用整个评估网格上的身份并集，每帧是否可达仍由共享图的时间位决定。
// 每次读取沿用前沿分批：超过阈值时按字段收窄宽读取，响应超过分批边界再按身份对半拆分。
func (loader *timeGraphMatrixLoader) loadTopologyHops(ctx context.Context, space string, source cmdb.Resource, identity cmdb.Matcher, relations []cmdb.Relation, wideGroups []topologyFetchGroup, hops int, reorder bool, queries *int) (err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-hop-fetch")
	defer span.End(&err)
	q := loader.graph
	fetch := &topologyHopFetch{
		loader: loader, space: space, source: source, identity: identity,
		reverse: map[timeGraphRelationKey]cmdb.Relation{}, whole: map[timeGraphRelationKey]bool{},
		expanded: map[uint64]bool{}, roots: map[uint64]bool{}, queries: queries,
	}
	levels := []int64{}
	defer func() {
		span.Set("hop-frontier-nodes-by-level", levels)
		span.Set("hop-read-count", fetch.reads)
		span.Set("hop-expanded-node-count", len(fetch.expanded))
		span.Set("hop-whole-relation-count", len(fetch.whole))
	}()

	// 缺物理路由的关系不访问后端，先按已读且无数据标记 partial：即使逐跳取数从未到达
	// 它的端点，质量状态也与整条读取一致。
	unrouted := map[timeGraphRelationKey]bool{}
	for _, group := range wideGroups {
		if !group.noRoute {
			continue
		}
		for _, relation := range group.relations {
			unrouted[timeGraphRelationKeyFor(relation)] = true
		}
		if err = loader.fetchTopologyGroup(group, space, queries, nil); err != nil {
			return err
		}
	}
	for _, relation := range relations {
		if !unrouted[timeGraphRelationKeyFor(relation)] {
			fetch.relations = append(fetch.relations, relation)
		}
	}
	// 关闭 Matrix 复用时，每条逻辑关系各自取数，不共用反向关系的响应。
	if SharedTopologyReuseMatrix {
		for _, relation := range fetch.relations {
			for _, other := range fetch.relations {
				if topologyReverseRelation(relation, other) {
					fetch.reverse[timeGraphRelationKeyFor(relation)] = other
					break
				}
			}
		}
	}

	observed := false
	groups, err := loader.compileTopologyFetches(ctx, space, fetch.rootInputs(), reorder)
	if err != nil {
		return err
	}
	span.Set("root-physical-group-count", len(groups))
	if err = loader.fetchTopologyGroups(groups, space, queries, func(_ cmdb.Relation, matrix pl.Matrix) {
		for _, series := range matrix {
			if len(series.Points) > 0 {
				observed = true
				return
			}
		}
	}); err != nil {
		return err
	}
	// 根的全部关联边已读入：后端完整且全窗无观测即为空邻域。缺路由的关系按已读且无数据
	// 处理，不妨碍判空，其原因仍留在各帧。
	if !observed && !q.backendPartial() {
		return nil
	}

	reachable, err := fetch.seeds(ctx)
	if err != nil {
		return err
	}
	frontier := maps.Clone(reachable)
	levels = append(levels, int64(len(frontier)))
	for level := 0; level < hops && len(frontier) > 0; level++ {
		if err = fetch.expand(ctx, frontier); err != nil {
			break
		}
		if frontier, err = fetch.step(ctx, frontier, reachable); err != nil {
			return err
		}
		levels = append(levels, int64(len(frontier)))
	}
	if err == nil {
		err = fetch.induce(ctx, reachable)
	}
	if errors.Is(err, errTopologyHopIncompleteIdentity) {
		// 已读入的边都属于整条读取的结果，补齐整条读取后答案不变。
		span.Set("fetch-mode", "hop-then-bulk")
		return loader.fetchTopologyGroups(wideGroups, space, queries, nil)
	}
	return err
}

// rootInputs 按根条件读取以源类型为任一端的候选关系。入边按目标端条件读取，
// 根即使只有入边也能被观测到；反向关系紧随原关系编排，以共用同一次物理响应。
func (fetch *topologyHopFetch) rootInputs() []topologyFetchInput {
	q := fetch.loader.graph
	type anchored struct {
		key    timeGraphRelationKey
		target bool
	}
	added := map[anchored]bool{}
	inputs := []topologyFetchInput{}
	add := func(relation cmdb.Relation, target bool) {
		// 静态自关联两端共用同一组标签，源端条件已覆盖目标端。
		if relation.V[0] == relation.V[1] && relation.Category == string(RelationCategoryStatic) {
			target = false
		}
		item := anchored{key: timeGraphRelationKeyFor(relation), target: target}
		if added[item] {
			return
		}
		added[item] = true
		filter := fetch.identity
		if target {
			filter = q.topologyAnchorFilter(relation, true, fetch.identity)
		}
		inputs = append(inputs, topologyFetchInput{relation: relation, filter: filter})
	}
	for _, relation := range fetch.relations {
		if relation.V[0] != fetch.source {
			continue
		}
		add(relation, false)
		if reverse, ok := fetch.reverse[timeGraphRelationKeyFor(relation)]; ok {
			add(reverse, true)
		}
	}
	for _, relation := range fetch.relations {
		if relation.V[1] == fetch.source {
			add(relation, true)
		}
	}
	return inputs
}

// rootCovered 表示节点命中根条件，首跳已读入它的全部关联边。
func (fetch *topologyHopFetch) rootCovered(node uint64) bool {
	q := fetch.loader.graph
	if q.nodeBuilder.resourceName(node) != fetch.source {
		return false
	}
	covered, ok := fetch.roots[node]
	if !ok {
		_, info := q.nodeBuilder.Info(node)
		covered = q.matchesPartial(info, fetch.identity)
		fetch.roots[node] = covered
	}
	return covered
}

func (fetch *topologyHopFetch) covered(node uint64) bool {
	return fetch.expanded[node] || fetch.rootCovered(node)
}

// seeds 用最终遍历的同一判定求根；首跳之后根的关联边不再变化。
func (fetch *topologyHopFetch) seeds(ctx context.Context) (map[uint64]timeBitmap, error) {
	q := fetch.loader.graph
	q.lock.RLock()
	defer q.lock.RUnlock()
	nodeBits, _, err := q.shared.state(ctx, q.shared.grid, SharedTopologyQuery{})
	if err != nil {
		return nil, err
	}
	return q.sharedTopologySeeds(ctx, q.shared.grid, SharedTopologyQuery{SourceType: fetch.source, SourceMatcher: fetch.identity, RootBits: q.sharedTopologyRootBits()}, nodeBits)
}

// step 用已读入的边推进一层。前沿节点的出边此前已全部读入，结果与整条读取时相同；
// 候选关系都在请求范围内，因此不再按类别、类型和方向过滤边。
func (fetch *topologyHopFetch) step(ctx context.Context, frontier, reachable map[uint64]timeBitmap) (map[uint64]timeBitmap, error) {
	q := fetch.loader.graph
	q.lock.RLock()
	defer q.lock.RUnlock()
	nodeBits, edges, err := q.shared.state(ctx, q.shared.grid, SharedTopologyQuery{})
	if err != nil {
		return nil, err
	}
	return sharedTopologyStep(ctx, frontier, reachable, nodeBits, edges)
}

// expand 读取前沿中尚未展开节点的全部出边。
func (fetch *topologyHopFetch) expand(ctx context.Context, frontier map[uint64]timeBitmap) error {
	nodes := make([]uint64, 0, len(frontier))
	for node := range frontier {
		if !fetch.covered(node) {
			nodes = append(nodes, node)
		}
	}
	if len(nodes) == 0 {
		return nil
	}
	identities, err := fetch.identitiesByType(nodes)
	if err != nil {
		return err
	}
	for _, relation := range fetch.relations {
		if len(identities[relation.V[0]]) == 0 || fetch.whole[timeGraphRelationKeyFor(relation)] {
			continue
		}
		if err := fetch.read(ctx, relation, false, identities[relation.V[0]]); err != nil {
			return err
		}
	}
	for _, node := range nodes {
		fetch.expanded[node] = true
	}
	return nil
}

// induce 补读两端都已到达、尚未读入的诱导边。起点只剩最后一层未展开的节点；
// 终点已展开时，成对反向关系的读取已带回这条边。每条关系从两端中身份较少的一端读取。
func (fetch *topologyHopFetch) induce(ctx context.Context, reachable map[uint64]timeBitmap) error {
	q := fetch.loader.graph
	reached, open := map[cmdb.Resource][]uint64{}, map[cmdb.Resource][]uint64{}
	for node := range reachable {
		resource := q.nodeBuilder.resourceName(node)
		reached[resource] = append(reached[resource], node)
		if !fetch.covered(node) {
			open[resource] = append(open[resource], node)
		}
	}
	done := map[timeGraphRelationKey]bool{}
	for _, relation := range fetch.relations {
		key := timeGraphRelationKeyFor(relation)
		sources := open[relation.V[0]]
		if done[key] || fetch.whole[key] || len(sources) == 0 {
			continue
		}
		reverse, paired := fetch.reverse[key]
		targets := make([]uint64, 0, len(reached[relation.V[1]]))
		for _, node := range reached[relation.V[1]] {
			if !fetch.rootCovered(node) && !(paired && fetch.expanded[node]) {
				targets = append(targets, node)
			}
		}
		if len(targets) == 0 {
			continue
		}
		anchor, anchorType, targetAnchor := sources, relation.V[0], false
		if len(targets) < len(sources) {
			anchor, anchorType, targetAnchor = targets, relation.V[1], true
		}
		identities, err := fetch.identitiesByType(anchor)
		if err != nil {
			return err
		}
		if err := fetch.read(ctx, relation, targetAnchor, identities[anchorType]); err != nil {
			return err
		}
		// 成对读取覆盖了反向关系所需的同一组端点对。
		if paired {
			done[timeGraphRelationKeyFor(reverse)] = true
		}
	}
	return nil
}

// identitiesByType 按资源类型取节点的完整主键，按身份键排序以固定取数顺序。
func (fetch *topologyHopFetch) identitiesByType(nodes []uint64) (map[cmdb.Resource][]cmdb.Matcher, error) {
	q := fetch.loader.graph
	type keyed struct {
		key      string
		identity cmdb.Matcher
	}
	byType := map[cmdb.Resource][]keyed{}
	for _, node := range nodes {
		resource, info := q.nodeBuilder.Info(node)
		identity := q.primaryMatcher(resource, info)
		if !q.completeFrontierIdentity(resource, identity) {
			return nil, errTopologyHopIncompleteIdentity
		}
		byType[resource] = append(byType[resource], keyed{key: q.primaryMatcherKey(resource, identity), identity: identity})
	}
	result := make(map[cmdb.Resource][]cmdb.Matcher, len(byType))
	for resource, items := range byType {
		sort.Slice(items, func(i, j int) bool { return items[i].key < items[j].key })
		identities := make([]cmdb.Matcher, len(items))
		for i, item := range items {
			identities[i] = item.identity
		}
		result[resource] = identities
	}
	return result, nil
}

// read 按锚点一端的身份读取一条关系，并把同一响应写入成对的反向关系。
func (fetch *topologyHopFetch) read(ctx context.Context, relation cmdb.Relation, targetAnchor bool, identities []cmdb.Matcher) error {
	loader := fetch.loader
	views := []cmdb.Relation{relation}
	if reverse, ok := fetch.reverse[timeGraphRelationKeyFor(relation)]; ok {
		views = append(views, reverse)
	}
	readCtx := newTimeGraphSubqueryContext(ctx)
	metadata.GetQueryParams(readCtx).SetIsSkipK8s(true)
	readCtx = withTimeGraphQueryStage(readCtx, "relation-edge")
	fetch.reads++
	whole, err := loader.loadFrontier(readCtx, fetch.space, relation, targetAnchor, identities, loader.applyTopologyRelations(views, nil), fetch.queries)
	if err != nil {
		return err
	}
	if whole {
		for _, view := range views {
			fetch.whole[timeGraphRelationKeyFor(view)] = true
		}
	}
	return nil
}
