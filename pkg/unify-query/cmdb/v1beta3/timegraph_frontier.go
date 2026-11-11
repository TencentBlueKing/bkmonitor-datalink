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
)

// 每个选择器最多包含 64 组完整身份，避免条件规模随图中的实例数无限增长。
const timeGraphFrontierBatchSize = 64

// timeGraphWideFrontierThreshold 是从按身份分批切换到宽读取的身份数。分批是顺序
// 查询，耗时随身份数线性增长；超过这个规模后一次宽读取的总代价更低。
const timeGraphWideFrontierThreshold = 2 * timeGraphFrontierBatchSize

// timeGraphBackendResponseBytes 是单次后端响应解压后的分批边界，不是能力上限：
// 超过它的读取按身份对半拆分（区间兼容入口另按时间对半拆分），总读取量可以大于它。
const timeGraphBackendResponseBytes int64 = 16 * 1024 * 1024

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

// frontierAnchorFields 把关系上的标签映射到锚点资源的主键字段。
func (q *TimeGraph) frontierAnchorFields(relation cmdb.Relation, targetAnchor bool) (cmdb.Resource, map[string]string) {
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
	return resource, anchorFields
}

// frontierQuery 即使从目标端观察关系，也保留原关系方向。
// 每个 OR 分组包含一组完整联合主键和端点条件；逐字段 IN 会错误匹配交叉组合。
func (loader *timeGraphMatrixLoader) frontierQuery(ctx context.Context, space string, relation cmdb.Relation, targetAnchor bool, identities []cmdb.Matcher) (*structured.QueryTs, error) {
	q := loader.graph
	ts, err := q.MakeQueryTsWithWindow(ctx, space, nil, loader.start, loader.end, loader.step, loader.lookBack, relation)
	if err != nil || ts == nil {
		return ts, err
	}
	resource, anchorFields := q.frontierAnchorFields(relation, targetAnchor)
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

// frontierScopeQuery 用一个查询覆盖整个前沿：取值不超过一批的主键字段收窄到取值
// 列表，其余字段不收窄。逐字段列表是联合主键的超集，调用方必须按身份过滤结果。
func (loader *timeGraphMatrixLoader) frontierScopeQuery(ctx context.Context, space string, relation cmdb.Relation, identities []cmdb.Matcher) (*structured.QueryTs, error) {
	q := loader.graph
	ts, err := q.MakeQueryTsWithWindow(ctx, space, nil, loader.start, loader.end, loader.step, loader.lookBack, relation)
	if err != nil || ts == nil {
		return ts, err
	}
	_, anchorFields := q.frontierAnchorFields(relation, false)
	values := make(map[string]map[string]struct{}, len(anchorFields))
	for _, primary := range anchorFields {
		values[primary] = make(map[string]struct{})
	}
	for _, identity := range identities {
		for primary, distinct := range values {
			if len(distinct) <= timeGraphFrontierBatchSize {
				distinct[identity[primary]] = struct{}{}
			}
		}
	}
	fields := ts.QueryList[0].Conditions.FieldList
	for i, field := range fields {
		primary, ok := anchorFields[field.DimensionName]
		if !ok || len(values[primary]) > timeGraphFrontierBatchSize {
			continue
		}
		list := make([]string, 0, len(values[primary]))
		for value := range values[primary] {
			list = append(list, value)
		}
		sort.Strings(list)
		field.Operator = structured.ConditionContains
		field.Value = list
		fields[i] = field
	}
	return ts, nil
}

// loadFrontier 读取路径下一跳。规模超过阈值时先尝试一次宽读取，响应超过分批边界再
// 退回分批：边界限制的是单次响应，分批读取的总量仍可以大于它。
func (loader *timeGraphMatrixLoader) loadFrontier(ctx context.Context, space string, relation cmdb.Relation, identities []cmdb.Matcher, apply func(context.Context, pl.Matrix) error, queries *int) error {
	if len(identities) > timeGraphWideFrontierThreshold {
		ts, err := loader.frontierScopeQuery(ctx, space, relation, identities)
		if err != nil {
			return err
		}
		if ts != nil {
			*queries = *queries + 1
			// 独立子查询：被拒收的读取留下的质量状态不能影响替代它的分批读取。
			budgeted := metadata.WithBackendResponseLimit(frontierSubqueryContext(ctx), timeGraphBackendResponseBytes)
			matrix, _, err := loader.query(budgeted, ts)
			if err == nil {
				return apply(ctx, matrix)
			}
			if !isBackendResponseTooLarge(err) {
				return err
			}
		}
	}
	for offset := 0; offset < len(identities); offset += timeGraphFrontierBatchSize {
		end := min(offset+timeGraphFrontierBatchSize, len(identities))
		if err := loader.loadFrontierBatch(ctx, space, relation, false, identities[offset:end], apply, queries); err != nil {
			return err
		}
	}
	return nil
}

// frontierSubqueryContext 给一次前沿读取独立的 metadata，保留当前查询阶段。
func frontierSubqueryContext(ctx context.Context) context.Context {
	child := newTimeGraphSubqueryContext(ctx)
	metadata.GetQueryParams(child).SetIsSkipK8s(true)
	return withTimeGraphQueryStage(child, timeGraphQueryStage(ctx))
}

func isBackendResponseTooLarge(err error) bool {
	var limitErr *ResultLimitError
	return errors.Is(err, metadata.ErrBackendResponseTooLarge) || (errors.As(err, &limitErr) && limitErr.Reason == "max_response_bytes")
}

// loadFrontierBatch 遇到过大响应时先拆分身份组，避免每个时间片重复读取整批候选。
// 单组身份仍走原有的精确时间网格分片，并共用请求预算。
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
	if !isBackendResponseTooLarge(err) {
		return err
	}
	middle := len(identities) / 2
	if err = loader.loadFrontierBatch(frontierSubqueryContext(ctx), space, relation, targetAnchor, identities[:middle], apply, queries); err != nil {
		return err
	}
	return loader.loadFrontierBatch(frontierSubqueryContext(ctx), space, relation, targetAnchor, identities[middle:], apply, queries)
}
