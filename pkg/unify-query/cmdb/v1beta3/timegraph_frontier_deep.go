// Copyright (C) 2022 THL A29 Limited, a Tencent company.
// Licensed under the MIT License.
package v1beta3

import (
	"context"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	pl "github.com/prometheus/prometheus/promql"
)

// loadDeepTopologyFrontier 仅对静态双向且属性投影互不依赖的关系缩小取数范围。
// 各帧身份的时间并集覆盖每一帧的可达节点，最终遍历仍使用原始逐帧位图。
// 多取一层边界关系，才能保留诱导子图中的非树边。
func (loader *timeGraphMatrixLoader) loadDeepTopologyFrontier(ctx context.Context, space string, source cmdb.Resource, identity cmdb.Matcher, relations []cmdb.Relation, hops int, reorder bool, queries *int) (bool, error) {
	q := loader.graph
	key := q.primaryMatcherKey(source, identity)
	first := map[string]cmdb.Matcher{key: q.primaryMatcher(source, identity)}
	seen := timeGraphFrontier{source: first}
	frontier := timeGraphFrontier{source: first}
	overflow, incomplete := false, false
	// 129 只是切换宽读取计划的哨兵，不限制结果；已取回的序列先全部写入同一张图。
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
		// 身份缺失或部分成功时无法证明候选并集完整，交给同一图上的宽读取补齐；
		// 已有的质量状态与累计取数成本继续保留。
		if overflow || incomplete || len(q.partialTimes) > 0 {
			return false, nil
		}
		frontier = next
	}
	return true, nil
}
