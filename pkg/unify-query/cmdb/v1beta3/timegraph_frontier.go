// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"

	"github.com/pkg/errors"
	pl "github.com/prometheus/prometheus/promql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// 每个选择器最多包含 64 组完整身份，避免条件规模随图中的实例数无限增长。
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

// frontierQuery 即使从目标端观察关系，也保留原关系方向。
// 每个 OR 分组包含一组完整联合主键和端点条件；逐字段 IN 会错误匹配交叉组合。
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
