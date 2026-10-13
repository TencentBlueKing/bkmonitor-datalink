// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"sort"

	"github.com/pkg/errors"
	pl "github.com/prometheus/prometheus/promql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type timeGraphFrontierHopsKey struct{}

// Bound each selector independently of the number of instances in the graph.
const timeGraphFrontierBatchSize = 64

type timeGraphFrontier map[cmdb.Resource]map[string]cmdb.Matcher

func (q *TimeGraph) completeFrontierIdentity(resource cmdb.Resource, info cmdb.Matcher) bool {
	fields := q.nodeBuilder.resourceIndexes(resource)
	if len(fields) == 0 {
		return false
	}
	for _, field := range fields {
		if info[field] == "" {
			return false
		}
	}
	return true
}

// Anchor selectors retain the original relation orientation, even when observing
// its target. Each OR group includes the complete identity AND endpoint guards;
// separate per-field IN selectors would admit crossed composite identities.
func (loader *timeGraphMatrixLoader) frontierQuery(ctx context.Context, space string, relation cmdb.Relation, targetAnchor bool, identities []cmdb.Matcher) (*structured.QueryTs, error) {
	q := loader.graph
	ts, err := q.MakeQueryTsWithWindow(ctx, space, nil, loader.start, loader.end, loader.step, loader.lookBack, relation)
	if err != nil || ts == nil {
		return ts, err
	}
	resource := relation.V[0]
	sourcePrefix, targetPrefix := q.relationEndpointPrefixes(relation, relation.V[0], relation.V[1])
	prefix := sourcePrefix
	if targetAnchor {
		resource, prefix = relation.V[1], targetPrefix
	}
	anchorFields := make(map[string]string)
	for _, field := range q.nodeBuilder.resourceIndexes(resource) {
		label := field
		if relation.Category == string(RelationCategoryDynamic) {
			label = prefix + field
		}
		anchorFields[label] = field
	}
	base := ts.QueryList[0].Conditions.FieldList
	conditions := structured.Conditions{}
	for _, identity := range identities {
		if !q.completeFrontierIdentity(resource, identity) {
			return nil, errors.New("frontier identity has incomplete primary fields")
		}
		for i, field := range base {
			if len(conditions.FieldList) > 0 {
				connector := structured.ConditionAnd
				if i == 0 {
					connector = structured.ConditionOr
				}
				conditions.ConditionList = append(conditions.ConditionList, connector)
			}
			if primary, ok := anchorFields[field.DimensionName]; ok {
				field.Operator = structured.ConditionEqual
				field.Value = []string{identity[primary]}
			}
			conditions.FieldList = append(conditions.FieldList, field)
		}
	}
	ts.QueryList[0].Conditions = conditions
	return ts, nil
}

// The frontier is a union over the entire grid, used only to bound fetching.
// Per-frame reachability remains the shared graph's bitmap intersection. Query
// H-boundary outgoing edges too, retaining induced edges outside the BFS tree.
func (loader *timeGraphMatrixLoader) loadTopologyFrontier(ctx context.Context, space string, source cmdb.Resource, identity cmdb.Matcher, relations []cmdb.Relation, hops int) (queries int, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-load-instance-frontier")
	defer span.End(&err)
	q := loader.graph
	// Validate every required route before an empty frontier can short-circuit it.
	// Matrix overrides intentionally skip route resolution, as in loader.query.
	if loader.override == nil {
		for _, relation := range relations {
			routeCtx := newTimeGraphSubqueryContext(ctx)
			metadata.GetQueryParams(routeCtx).SetIsSkipK8s(true)
			ts, queryErr := q.MakeQueryTsWithWindow(routeCtx, space, nil, loader.start, loader.end, loader.step, loader.lookBack, relation)
			if queryErr != nil {
				return queries, queryErr
			}
			if ts == nil {
				continue
			}
			if _, _, routeErr := loader.model.prepareTimeGraphVMQuery(withTimeGraphQueryStage(routeCtx, "relation-edge"), ts); routeErr != nil {
				return queries, routeErr
			}
		}
	}
	seen := timeGraphFrontier{}
	add := func(frontier timeGraphFrontier, resource cmdb.Resource, info cmdb.Matcher) {
		if !q.completeFrontierIdentity(resource, info) {
			return
		}
		key := q.primaryMatcherKey(resource, info)
		if _, exists := seen[resource][key]; exists {
			return
		}
		if seen[resource] == nil {
			seen[resource] = make(map[string]cmdb.Matcher)
		}
		if frontier[resource] == nil {
			frontier[resource] = make(map[string]cmdb.Matcher)
		}
		primary := q.primaryMatcher(resource, info)
		seen[resource][key], frontier[resource][key] = primary, primary
	}
	frontier := timeGraphFrontier{}
	add(frontier, source, identity)
	targetMatchers := make(map[cmdb.Resource]map[string]cmdb.Matcher)
	targetTimes := make(map[int64]map[cmdb.Resource]map[string]struct{})
	load := func(relation cmdb.Relation, targetAnchor bool, identities []cmdb.Matcher, next timeGraphFrontier) error {
		queryCtx := newTimeGraphSubqueryContext(ctx)
		metadata.GetQueryParams(queryCtx).SetIsSkipK8s(true)
		queryCtx = withTimeGraphQueryStage(queryCtx, "relation-edge")
		return loader.loadFrontierBatch(queryCtx, space, relation, targetAnchor, identities, func(shardCtx context.Context, matrix pl.Matrix) error {
			if applyErr := q.applyRelationMatrix(shardCtx, matrix, relation, targetMatchers, targetTimes); applyErr != nil {
				return applyErr
			}
			if next == nil || targetAnchor {
				return nil
			}
			dynamic := relation.Category == string(RelationCategoryDynamic)
			_, prefix := q.relationEndpointPrefixes(relation, relation.V[0], relation.V[1])
			for _, series := range matrix {
				if err := ctx.Err(); err != nil {
					return err
				}
				if len(series.Points) == 0 {
					continue
				}
				info := make(cmdb.Matcher, len(series.Metric))
				for _, label := range series.Metric {
					info[label.Name] = label.Value
				}
				add(next, relation.V[1], q.relationEndpointInfo(info, relation.V[1], prefix, dynamic))
			}
			return nil
		}, &queries)
	}
	// Incoming-only roots must still be observed, without traversing against the
	// planned direction. Same-type dynamic relations also need the target probe.
	for _, relation := range relations {
		if len(relation.V) == 2 && relation.V[1] == source {
			if err = load(relation, true, []cmdb.Matcher{identity}, nil); err != nil {
				return queries, err
			}
		}
	}
	for depth := 0; depth <= hops && len(frontier) > 0; depth++ {
		if err = ctx.Err(); err != nil {
			return queries, err
		}
		next := timeGraphFrontier{}
		for _, relation := range relations {
			if len(relation.V) != 2 {
				continue
			}
			candidates := frontier[relation.V[0]]
			keys := make([]string, 0, len(candidates))
			for key := range candidates {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for offset := 0; offset < len(keys); offset += timeGraphFrontierBatchSize {
				end := min(offset+timeGraphFrontierBatchSize, len(keys))
				batch := make([]cmdb.Matcher, 0, end-offset)
				for _, key := range keys[offset:end] {
					batch = append(batch, candidates[key])
				}
				expand := next
				if depth == hops {
					expand = nil
				}
				if err = load(relation, false, batch, expand); err != nil {
					return queries, err
				}
			}
		}
		frontier = next
	}
	span.Set("frontier-query-count", queries)
	return queries, nil
}

// Prefer splitting identity tuples before time: this prevents a wide selector
// from rereading a large candidate set at every timestamp. A single identity
// retains the existing bounded exact-grid time sharding and shared budgets.
func (loader *timeGraphMatrixLoader) loadFrontierBatch(ctx context.Context, space string, relation cmdb.Relation, targetAnchor bool, identities []cmdb.Matcher, apply func(context.Context, pl.Matrix) error, queries *int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ts, err := loader.frontierQuery(ctx, space, relation, targetAnchor, identities)
	if err != nil {
		return err
	}
	*queries = *queries + 1
	if len(identities) == 1 {
		return loader.queryAndApply(ctx, ts, apply)
	}
	matrix, _, err := loader.query(ctx, ts)
	if err == nil {
		return apply(ctx, matrix)
	}
	var limitErr *ResultLimitError
	if !errors.Is(err, metadata.ErrBackendResponseTooLarge) && !(errors.As(err, &limitErr) && limitErr.Reason == "max_response_bytes") {
		return err
	}
	middle := len(identities) / 2
	child := func() context.Context {
		childCtx := newTimeGraphSubqueryContext(ctx)
		metadata.GetQueryParams(childCtx).SetIsSkipK8s(true)
		return withTimeGraphQueryStage(childCtx, timeGraphQueryStage(ctx))
	}
	if err = loader.loadFrontierBatch(child(), space, relation, targetAnchor, identities[:middle], apply, queries); err != nil {
		return err
	}
	return loader.loadFrontierBatch(child(), space, relation, targetAnchor, identities[middle:], apply, queries)
}
