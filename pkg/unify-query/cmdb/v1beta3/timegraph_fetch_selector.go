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
}

// Routes, projections, conditions, user and backend semantics all participate
// in grouping. Preparation runs before any empty-frontier shortcut.
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
			// Range constraints can belong to either endpoint. MakeQueryTs accepts only
			// source identity filters, so set the proven projected labels explicitly.
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
		if loader.override == nil {
			expression, params, err = loader.model.prepareTimeGraphVMQuery(queryCtx, ts)
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
			groups = append(groups, topologyFetchGroup{input: input, ctx: queryCtx, query: ts, relations: []cmdb.Relation{input.relation}})
		}
	}
	return groups, nil
}

func (loader *timeGraphMatrixLoader) reserveRelationApplication(ctx context.Context, matrix pl.Matrix) error {
	size := timeGraphReturnedMatrixSize(matrix)
	loader.graphEstimatedBytes += int64(size.Points)*256 + int64(size.Series)*1024 + int64(size.LabelBytes)*8
	return ReserveTopologyStage(ctx, "graph", loader.graphEstimatedBytes)
}

func (loader *timeGraphMatrixLoader) fetchTopologyGroup(group topologyFetchGroup, space string, queries *int, collect func(cmdb.Relation, pl.Matrix)) error {
	apply := func(ctx context.Context, matrix pl.Matrix) error {
		for i, relation := range group.relations {
			if err := ctx.Err(); err != nil {
				return err
			}
			if i > 0 {
				loader.reusedQueryCount++
				// One physical input still incurs each logical graph application.
				if _, err := loader.validateMatrix(ctx, matrix, nil); err != nil {
					return err
				}
				if err := loader.reserveRelationApplication(ctx, matrix); err != nil {
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
	if len(group.input.identities) > 0 {
		return loader.loadFrontierBatch(group.ctx, space, group.input.relation, group.input.targetAnchor, group.input.identities, apply, queries)
	}
	*queries++
	return loader.queryAndApply(group.ctx, group.query, apply)
}

func (loader *timeGraphMatrixLoader) loadSelectedTopology(ctx context.Context, space string, source cmdb.Resource, identity cmdb.Matcher, rootRelations map[timeGraphRelationKey]struct{}, relations []cmdb.Relation, plan timeGraphFetchPlan) (queries int, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-select-fetch")
	defer span.End(&err)
	q := loader.graph
	// A relation beyond the traversal boundary may still supply a projected
	// attribute to a retained endpoint. Restore the original ordered wide plan
	// when projection independence cannot be proved before type pruning.
	allowedComponent := topologyRelationComponent(source, plan.allowed)
	if !q.topologyProjectionCompatible(allowedComponent) {
		relations = allowedComponent
	}
	mode, component := q.topologyFetchMode(source, identity, relations, plan)
	span.Set("fetch-mode", mode)
	span.Set("max-hops", plan.hops)
	reorder := len(component) > 0 && topologyStaticBidirectional(component) && q.topologyProjectionCompatible(relations)
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

	if mode == "star" {
		inputs := []topologyFetchInput{}
		for _, relation := range relations {
			inputs = append(inputs, topologyFetchInput{relation: relation, identities: []cmdb.Matcher{identity}, targetAnchor: relation.V[1] == source})
		}
		groups, compileErr := loader.compileTopologyFetches(ctx, space, inputs, reorder)
		if compileErr != nil {
			return queries, compileErr
		}
		for _, group := range groups {
			if err = loader.fetchTopologyGroup(group, space, &queries, nil); err != nil {
				return queries, err
			}
		}
		return queries, nil
	}

	queriedRoots := map[timeGraphRelationKey]struct{}{}
	observed := false
	first := timeGraphFrontier{}
	incompleteIdentity := false
	if topologyRootProbeCoversEndpoints(source, relations) {
		inputs := []topologyFetchInput{}
		for _, relation := range relations {
			if relation.V[0] != source {
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
				// 129 is an overflow sentinel, not a result limit. Graph application
				// always retains every returned series; overflowing groups read wide.
				if len(candidates) <= 2*timeGraphFrontierBatchSize {
					candidates[q.primaryMatcherKey(relation.V[1], info)] = q.primaryMatcher(relation.V[1], info)
				}
			}
		}
		for _, group := range groups {
			if err = loader.fetchTopologyGroup(group, space, &queries, collect); err != nil {
				return queries, err
			}
		}
		if !observed && len(q.partialTimes) == 0 {
			return queries, nil
		}
	}
	if mode == "bipartite" && len(q.partialTimes) == 0 && !incompleteIdentity {
		inputs := []topologyFetchInput{}
		for _, relation := range relations {
			candidates, ok := first[relation.V[0]]
			targetAnchor := false
			if !ok {
				candidates, ok = first[relation.V[1]]
				targetAnchor = true
			}
			if !ok || len(candidates) > 2*timeGraphFrontierBatchSize {
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
		for _, group := range groups {
			if err = loader.fetchTopologyGroup(group, space, &queries, nil); err != nil {
				return queries, err
			}
		}
		return queries, nil
	}
	// Partial first-stage input cannot prove the Y union complete. Continue on
	// the same graph and loader; retain quality, attributes and cumulative cost.
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
	for _, group := range groups {
		if err = loader.fetchTopologyGroup(group, space, &queries, nil); err != nil {
			return queries, err
		}
	}
	return queries, nil
}
