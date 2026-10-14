// Copyright (C) 2022 THL A29 Limited, a Tencent company.
// Licensed under the MIT License.
package v1beta3

import (
	"context"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	pl "github.com/prometheus/prometheus/promql"
)

// Narrow only static bidirectional, projection-independent components. The
// time-union frontier is a superset of every frame's reachable identities;
// traversal still uses the original per-frame bits. Fetch the boundary too,
// retaining induced edges rather than only a traversal tree.
func (loader *timeGraphMatrixLoader) loadDeepTopologyFrontier(ctx context.Context, space string, source cmdb.Resource, identity cmdb.Matcher, relations []cmdb.Relation, hops int, reorder bool, queries *int) (bool, error) {
	q := loader.graph
	key := q.primaryMatcherKey(source, identity)
	first := map[string]cmdb.Matcher{key: q.primaryMatcher(source, identity)}
	seen := timeGraphFrontier{source: first}
	frontier := timeGraphFrontier{source: first}
	overflow, incomplete := false, false
	// This is a query-plan crossover, not an answer limit. As in the two-hop
	// plan, 129 is a sentinel: all fetched series are applied before reading wide.
	add := func(resource cmdb.Resource, info cmdb.Matcher, next timeGraphFrontier) {
		if !q.completeFrontierIdentity(resource, info) {
			incomplete = true
			return
		}
		key := q.primaryMatcherKey(resource, info)
		if seen[resource] == nil {
			seen[resource] = map[string]cmdb.Matcher{}
		}
		if _, ok := seen[resource][key]; ok {
			return
		}
		if len(seen[resource]) > 2*timeGraphFrontierBatchSize {
			overflow = true
			return
		}
		owned := q.primaryMatcher(resource, info)
		seen[resource][key] = owned
		if next[resource] == nil {
			next[resource] = map[string]cmdb.Matcher{}
		}
		next[resource][key] = owned
		if len(seen[resource]) > 2*timeGraphFrontierBatchSize {
			overflow = true
		}
	}
	for depth := 0; depth <= hops && len(frontier) > 0; depth++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		inputs := []topologyFetchInput{}
		for _, relation := range relations {
			for endpoint, resource := range relation.V {
				if endpoint == 1 && relation.V[0] == relation.V[1] {
					continue
				}
				candidates := frontier[resource]
				keys := make([]string, 0, len(candidates))
				for key := range candidates {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for offset := 0; offset < len(keys); offset += timeGraphFrontierBatchSize {
					identities := make([]cmdb.Matcher, 0, min(timeGraphFrontierBatchSize, len(keys)-offset))
					for _, key := range keys[offset:min(offset+timeGraphFrontierBatchSize, len(keys))] {
						identities = append(identities, candidates[key])
					}
					inputs = append(inputs, topologyFetchInput{relation: relation, identities: identities, targetAnchor: endpoint == 1})
				}
			}
		}
		groups, err := loader.compileTopologyFetches(ctx, space, inputs, reorder)
		if err != nil {
			return false, err
		}
		next := timeGraphFrontier{}
		labels := cmdb.Matcher{}
		collect := func(relation cmdb.Relation, matrix pl.Matrix) {
			for _, series := range matrix {
				if ctx.Err() != nil {
					return
				}
				if len(series.Points) == 0 {
					continue
				}
				clear(labels)
				for _, label := range series.Metric {
					labels[label.Name] = label.Value
				}
				for _, resource := range relation.V {
					add(resource, q.relationEndpointInfo(labels, resource, "", false), next)
				}
			}
		}
		for _, group := range groups {
			if err = loader.fetchTopologyGroup(group, space, queries, collect); err != nil {
				return false, err
			}
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		// Missing/partial input cannot prove the identity union complete. Continue
		// on the same graph and metadata, retaining all quality and cumulative cost.
		if overflow || incomplete || len(q.partialTimes) > 0 {
			return false, nil
		}
		frontier = next
	}
	return true, nil
}
