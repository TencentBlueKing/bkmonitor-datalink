// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"sort"

	pl "github.com/prometheus/prometheus/promql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type timeGraphLegacyPathKey struct{}

// The query selector uses the union of identities across the window. The
// per-timestamp frontier below still decides which returned edges can be used.
type legacyPathFrontier map[int64]map[string]cmdb.Matcher

func (frontier legacyPathFrontier) identities() []cmdb.Matcher {
	byKey := make(map[string]cmdb.Matcher)
	for _, bucket := range frontier {
		for key, identity := range bucket {
			byKey[key] = identity
		}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	identities := make([]cmdb.Matcher, 0, len(keys))
	for _, key := range keys {
		identities = append(identities, byKey[key])
	}
	return identities
}

// loadLegacyPath loads one ordered candidate, keeping subsequent reads bounded
// by the actual endpoints from the preceding hop. The general graph loader is
// still used for topology and callers supplying multiple relation paths.
func (loader *timeGraphMatrixLoader) loadLegacyPath(ctx context.Context, space string, path cmdb.RelationPath, sourceInfo cmdb.Matcher, targetMatchers map[cmdb.Resource]map[string]cmdb.Matcher, targetTimes map[int64]map[cmdb.Resource]map[string]struct{}) (queries int, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-load-legacy-path")
	defer span.End(&err)
	span.Set("path-hop-count", len(path.Steps)-1)
	var frontier legacyPathFrontier
	for hop := 1; hop < len(path.Steps); hop++ {
		if err := ctx.Err(); err != nil {
			return queries, err
		}
		next := make(legacyPathFrontier)
		identities := frontier.identities()
		if hop > 1 && len(identities) == 0 {
			// A partial empty root remains partial in graph.partialTimes. There
			// are no known reachable endpoints to expand, even in that case.
			span.Set("empty-frontier-hop", hop)
			break
		}
		source, target := path.Steps[hop-1].ResourceType, path.Steps[hop].ResourceType
		for _, relation := range loader.model.timeGraphRelationCandidates(space, source, target, path.Steps[hop]) {
			relationCtx := newTimeGraphSubqueryContext(ctx)
			metadata.GetQueryParams(relationCtx).SetIsSkipK8s(true)
			relationCtx = withTimeGraphQueryStage(relationCtx, "relation-edge")
			apply := func(applyCtx context.Context, matrix pl.Matrix) error {
				filtered, filterErr := loader.graph.filterLegacyPathMatrix(applyCtx, matrix, relation, frontier, next)
				if filterErr != nil {
					return filterErr
				}
				return loader.graph.applyRelationMatrix(applyCtx, filtered, relation, targetMatchers, targetTimes)
			}
			if hop == 1 {
				query, queryErr := loader.graph.MakeQueryTsWithWindow(relationCtx, space, sourceInfo, loader.start, loader.end, loader.step, loader.lookBack, relation)
				if queryErr != nil {
					return queries, queryErr
				}
				queries++
				if err := loader.queryAndApply(relationCtx, query, apply); err != nil {
					return queries, err
				}
			} else {
				for offset := 0; offset < len(identities); offset += timeGraphFrontierBatchSize {
					end := min(offset+timeGraphFrontierBatchSize, len(identities))
					if err := loader.loadFrontierBatch(relationCtx, space, relation, false, identities[offset:end], apply, &queries); err != nil {
						return queries, err
					}
				}
			}
		}
		frontier = next
	}
	span.Set("relation-query-count", queries)
	return queries, nil
}

func (q *TimeGraph) filterLegacyPathMatrix(ctx context.Context, matrix pl.Matrix, relation cmdb.Relation, previous, next legacyPathFrontier) (pl.Matrix, error) {
	source, target := relation.V[0], relation.V[1]
	sourcePrefix, targetPrefix := q.relationEndpointPrefixes(relation, source, target)
	dynamic := relation.Category == string(RelationCategoryDynamic)
	filtered := make(pl.Matrix, 0, len(matrix))
	for _, series := range matrix {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(series.Points) == 0 {
			continue
		}
		info := make(cmdb.Matcher, len(series.Metric))
		for _, label := range series.Metric {
			info[label.Name] = label.Value
		}
		sourceInfo := q.relationEndpointInfo(info, source, sourcePrefix, dynamic)
		targetInfo := q.relationEndpointInfo(info, target, targetPrefix, dynamic)
		if !q.completeFrontierIdentity(source, sourceInfo) || !q.completeFrontierIdentity(target, targetInfo) {
			return nil, fmt.Errorf("relation %s has incomplete endpoint primary fields", relation.RelationType)
		}
		sourceKey := q.primaryMatcherKey(source, sourceInfo)
		targetKey := q.primaryMatcherKey(target, targetInfo)
		identity := q.primaryMatcher(target, targetInfo)
		// Do not edit the source Matrix: it may be shared by query reuse.
		copyOfSeries := series
		copyOfSeries.Points = make([]pl.Point, 0, len(series.Points))
		for _, point := range series.Points {
			if previous != nil {
				if _, reachable := previous[point.T][sourceKey]; !reachable {
					continue
				}
			}
			copyOfSeries.Points = append(copyOfSeries.Points, point)
			if next[point.T] == nil {
				next[point.T] = make(map[string]cmdb.Matcher)
			}
			next[point.T][targetKey] = identity
		}
		if len(copyOfSeries.Points) > 0 {
			filtered = append(filtered, copyOfSeries)
		}
	}
	return filtered, nil
}
