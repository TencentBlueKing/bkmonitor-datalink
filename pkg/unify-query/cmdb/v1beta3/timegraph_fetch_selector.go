// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.
package v1beta3

import (
	"context"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	pl "github.com/prometheus/prometheus/promql"
)

type topologyFetchInput struct {
	relation     cmdb.Relation
	filter       cmdb.Matcher
	identities   []cmdb.Matcher
	targetAnchor bool
}
type topologyFetchGroup struct {
	input     topologyFetchInput
	ctx       context.Context
	query     *structured.QueryTs
	relations []cmdb.Relation
	// noRoute 表示该关系缺少物理路由：取数不访问后端，只按已读且无数据标记 partial。
	noRoute bool
}

// compileTopologyFetches 仅合并路由、投影、条件、用户和后端语义均相同的物理查询。
// 即使前沿为空也先完成查询准备，其余准备错误仍明确报错；缺少物理路由的关系单独成组、
// 不参与合并，取数时由 queryWithQuality 按已读且无数据处理并标记 partial。
func (loader *timeGraphMatrixLoader) compileTopologyFetches(ctx context.Context, space string, inputs []topologyFetchInput, reorder bool) ([]topologyFetchGroup, error) {
	groups := []topologyFetchGroup{}
	indexes := map[string]int{}
	for _, input := range inputs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		queryCtx := newTimeGraphSubqueryContext(ctx)
		metadata.GetQueryParams(queryCtx).SetIsSkipK8s(true)
		queryCtx = withTimeGraphQueryStage(queryCtx, "relation-edge")
		var ts *structured.QueryTs
		var err error
		if len(input.identities) > 0 {
			ts, err = loader.frontierQuery(queryCtx, space, input.relation, input.targetAnchor, input.identities)
		} else {
			ts, err = loader.graph.MakeQueryTsWithWindow(queryCtx, space, input.filter, loader.start, loader.end, loader.step, loader.lookBack, input.relation)
			// 范围条件可能落在关系的任一端；MakeQueryTs 只接受源端身份，
			// 因此仅显式写入已证明可投影的标签条件。
			if ts != nil {
				for i, field := range ts.QueryList[0].Conditions.FieldList {
					if value, ok := input.filter[field.DimensionName]; ok {
						field.Operator = structured.ConditionEqual
						field.Value = []string{value}
						ts.QueryList[0].Conditions.FieldList[i] = field
					}
				}
			}
		}
		if err != nil {
			return nil, err
		}
		if ts == nil {
			continue
		}
		ts.NotTimeAlign = metadata.IsExactTimeGrid(queryCtx)
		expression := ""
		var params *metadata.QueryParams
		noRoute := false
		if loader.override == nil {
			expression, params, err = loader.model.prepareTimeGraphVMQuery(queryCtx, ts)
			if loader.relationNoRouteReason(err) != "" {
				// params 为空，复用键保持为空，此关系不会并入其他物理查询。
				err, noRoute = nil, true
			}
			if err != nil {
				return nil, err
			}
		}
		key := ""
		if SharedTopologyReuseMatrix && (loader.override != nil || (params != nil && params.StorageType != nil && params.IsDirectQuery())) {
			key = loader.matrixReuseKey(queryCtx, ts, expression, params)
		}
		if index, ok := indexes[key]; key != "" && ok && (reorder || index == len(groups)-1) {
			groups[index].relations = append(groups[index].relations, input.relation)
		} else {
			if key != "" {
				indexes[key] = len(groups)
			}
			groups = append(groups, topologyFetchGroup{input: input, ctx: queryCtx, query: ts, relations: []cmdb.Relation{input.relation}, noRoute: noRoute})
		}
	}
	return groups, nil
}

// applyTopologyRelations 把一次物理响应写入共用它的每条逻辑关系。
func (loader *timeGraphMatrixLoader) applyTopologyRelations(relations []cmdb.Relation, collect func(cmdb.Relation, pl.Matrix)) func(context.Context, pl.Matrix) error {
	return func(ctx context.Context, matrix pl.Matrix) error {
		for i, relation := range relations {
			if err := ctx.Err(); err != nil {
				return err
			}
			if i > 0 {
				loader.reusedQueryCount++
				// 共用一次物理取数，但每条逻辑关系仍分别计量并写入图。
				if _, err := loader.validateMatrix(ctx, matrix, nil); err != nil {
					return err
				}
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if err := loader.graph.applyRelationMatrix(ctx, matrix, relation, nil, nil); err != nil {
				return err
			}
			if collect != nil {
				collect(relation, matrix)
			}
		}
		return nil
	}
}

func (loader *timeGraphMatrixLoader) fetchTopologyGroup(group topologyFetchGroup, space string, queries *int, collect func(cmdb.Relation, pl.Matrix)) error {
	apply := loader.applyTopologyRelations(group.relations, collect)
	if len(group.input.identities) > 0 {
		return loader.loadFrontierBatch(group.ctx, space, group.input.relation, group.input.targetAnchor, group.input.identities, apply, queries)
	}
	*queries++
	return loader.queryAndApply(group.ctx, group.query, apply)
}

// Each physical response is applied before the next group is fetched. Reuse
// and cumulative budgets stay with the request's single loader.
func (loader *timeGraphMatrixLoader) fetchTopologyGroups(groups []topologyFetchGroup, space string, queries *int, collect func(cmdb.Relation, pl.Matrix)) error {
	for _, group := range groups {
		if err := loader.fetchTopologyGroup(group, space, queries, collect); err != nil {
			return err
		}
	}
	return nil
}

func (loader *timeGraphMatrixLoader) loadSelectedTopology(ctx context.Context, space string, source cmdb.Resource, identity cmdb.Matcher, rootRelations map[timeGraphRelationKey]struct{}, relations []cmdb.Relation, plan timeGraphFetchPlan) (queries int, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-select-fetch")
	defer span.End(&err)
	q := loader.graph
	// H 跳边界外的关系也可能给保留节点提供属性。无法证明投影独立时，宽读取在按资源类型
	// 裁剪之前恢复整个关系分量；逐跳取数仍只读计划内的关系，之后按到达节点补读投影可见
	// 端点属性的关系，答案相同，不因属性投影冲突退回宽读取。
	allowedComponent := topologyRelationComponent(source, plan.allowed)
	visible := topologyReachableTypes(source, plan.hops, plan.allowed)
	planned := relations
	var attributes []topologyAttributeRead
	if !q.topologyProjectionCompatible(allowedComponent, visible) {
		relations = allowedComponent
		attributes = q.topologyAttributeReads(allowedComponent, visible)
	}
	span.Set("attribute-relation-count", len(attributes))
	mode, component := q.topologyFetchMode(source, identity, relations, plan)
	span.Set("fetch-mode", mode)
	span.Set("max-hops", plan.hops)
	reorder := topologyStaticBidirectional(relations) && q.topologyProjectionCompatible(relations, visible)
	filters := q.topologyRangeFilters(source, identity, component, relations, plan.hops)
	span.Set("range-filter-relation-count", len(filters))
	wideInputs := []topologyFetchInput{}
	for _, relation := range relations {
		wideInputs = append(wideInputs, topologyFetchInput{relation: relation, filter: filters[timeGraphRelationKeyFor(relation)]})
	}
	wideGroups, err := loader.compileTopologyFetches(ctx, space, wideInputs, reorder)
	if err != nil {
		return queries, err
	}
	span.Set("wide-physical-group-count", len(wideGroups))
	// An unfiltered root already needs the complete relation component; an empty
	// root is still empty after the full read and final traversal.
	if len(identity) == 0 && mode == "bulk" {
		err = loader.fetchTopologyGroups(wideGroups, space, &queries, nil)
		return queries, err
	}

	if mode == "star" {
		inputs := []topologyFetchInput{}
		for _, relation := range relations {
			inputs = append(inputs, topologyFetchInput{relation: relation, identities: []cmdb.Matcher{identity}, targetAnchor: relation.V[1] == source})
		}
		groups, compileErr := loader.compileTopologyFetches(ctx, space, inputs, reorder)
		if compileErr != nil {
			return queries, compileErr
		}
		err = loader.fetchTopologyGroups(groups, space, &queries, nil)
		return queries, err
	}
	// 其余有根条件的多跳计划从根逐跳读取实际到达的候选，不再整条读取关系分量。
	hopEligible := q.topologyHopFetchEligible(source, identity, planned)
	if mode == "bulk" && hopEligible {
		span.Set("fetch-mode", "hop")
		err = loader.loadTopologyHops(ctx, space, source, identity, planned, wideGroups, plan.hops, reorder, attributes, &queries)
		return queries, err
	}

	queriedRoots := map[timeGraphRelationKey]struct{}{}
	observed := false
	first := timeGraphFrontier{}
	incompleteIdentity := false
	if topologyRootProbeCoversEndpoints(source, relations) {
		inputs := []topologyFetchInput{}
		// An unfiltered root probe already reads the full physical relation.
		// Include its safe reverse view now to share the same physical response.
		probeBoth := len(identity) == 0 && topologyStaticBidirectional(relations) && q.topologyProjectionCompatible(relations, nil)
		for _, relation := range relations {
			if relation.V[0] != source && !(probeBoth && relation.V[1] == source) {
				continue
			}
			first[relation.V[1]] = map[string]cmdb.Matcher{}
			inputs = append(inputs, topologyFetchInput{relation: relation, filter: identity})
		}
		groups, compileErr := loader.compileTopologyFetches(ctx, space, inputs, reorder)
		if compileErr != nil {
			return queries, compileErr
		}
		collect := func(relation cmdb.Relation, matrix pl.Matrix) {
			queriedRoots[timeGraphRelationKeyFor(relation)] = struct{}{}
			for _, series := range matrix {
				if len(series.Points) == 0 {
					continue
				}
				observed = true
				if mode != "bipartite" {
					continue
				}
				labels := cmdb.Matcher{}
				for _, label := range series.Metric {
					labels[label.Name] = label.Value
				}
				info := q.relationEndpointInfo(labels, relation.V[1], "", false)
				if !q.completeFrontierIdentity(relation.V[1], info) {
					incompleteIdentity = true
					continue
				}
				candidates := first[relation.V[1]]
				// 超过阈值的一个候选只是切换宽读取的哨兵，不截断结果；取回的序列始终全部写入图。
				if len(candidates) <= timeGraphWideFrontierThreshold {
					candidates[q.primaryMatcherKey(relation.V[1], info)] = q.primaryMatcher(relation.V[1], info)
				}
			}
		}
		if err = loader.fetchTopologyGroups(groups, space, &queries, collect); err != nil {
			return queries, err
		}
		// 后端完整且全窗无根观测即为空邻域；缺路由的关系按已读且无数据处理，不妨碍判空。
		if !observed && !q.backendPartial() {
			return queries, nil
		}
	}
	if mode == "bipartite" && !incompleteIdentity {
		// 首阶段后端不完整时另一侧候选不完整，首跳候选超过阈值时按身份分批代价过高：两种情况
		// 都从根逐跳读取实际到达的候选，不退回整类关系的宽读取。
		large := false
		for _, candidates := range first {
			large = large || len(candidates) > timeGraphWideFrontierThreshold
		}
		if (q.backendPartial() || large) && hopEligible {
			span.Set("fetch-mode", "bipartite-then-hop")
			err = loader.loadTopologyHops(ctx, space, source, identity, relations, wideGroups, plan.hops, reorder, nil, &queries)
			return queries, err
		}
	}
	if mode == "bipartite" && !q.backendPartial() && !incompleteIdentity {
		inputs := []topologyFetchInput{}
		for _, relation := range relations {
			candidates, ok := first[relation.V[0]]
			targetAnchor := false
			if !ok {
				candidates, ok = first[relation.V[1]]
				targetAnchor = true
			}
			if !ok || len(candidates) > timeGraphWideFrontierThreshold {
				inputs = append(inputs, topologyFetchInput{relation: relation, filter: filters[timeGraphRelationKeyFor(relation)]})
				continue
			}
			if len(candidates) == 0 {
				continue
			}
			keys := make([]string, 0, len(candidates))
			for key := range candidates {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for offset := 0; offset < len(keys); offset += timeGraphFrontierBatchSize {
				batch := []cmdb.Matcher{}
				for _, key := range keys[offset:min(offset+timeGraphFrontierBatchSize, len(keys))] {
					batch = append(batch, candidates[key])
				}
				inputs = append(inputs, topologyFetchInput{relation: relation, identities: batch, targetAnchor: targetAnchor})
			}
		}
		groups, compileErr := loader.compileTopologyFetches(ctx, space, inputs, reorder)
		if compileErr != nil {
			return queries, compileErr
		}
		span.Set("second-stage-physical-group-count", len(groups))
		err = loader.fetchTopologyGroups(groups, space, &queries, nil)
		return queries, err
	}
	// 首阶段部分成功不能证明二部图另一侧候选完整，继续用同一图和取数器宽读取；
	// 已有属性、质量状态及累计成本均保留。
	if mode == "bipartite" {
		span.Set("fetch-mode", "bulk-after-partial")
	}
	remaining := []topologyFetchInput{}
	for _, input := range wideInputs {
		key := timeGraphRelationKeyFor(input.relation)
		if _, queried := queriedRoots[key]; queried {
			if _, safe := rootRelations[key]; safe || len(identity) == 0 {
				continue
			}
		}
		remaining = append(remaining, input)
	}
	groups, compileErr := loader.compileTopologyFetches(ctx, space, remaining, reorder)
	if compileErr != nil {
		return queries, compileErr
	}
	err = loader.fetchTopologyGroups(groups, space, &queries, nil)
	return queries, err
}
