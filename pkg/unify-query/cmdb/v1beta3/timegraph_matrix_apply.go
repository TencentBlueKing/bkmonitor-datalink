// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"time"

	"github.com/pkg/errors"
	pl "github.com/prometheus/prometheus/promql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

func (q *TimeGraph) applyRelationMatrix(ctx context.Context, matrix pl.Matrix, relation cmdb.Relation, targetMatchersByType map[cmdb.Resource]map[string]cmdb.Matcher, targetIDsByTimestamp map[int64]map[cmdb.Resource]map[string]struct{}) error {
	return q.applyRelationMatrixWithReverse(ctx, matrix, relation, nil, targetMatchersByType, targetIDsByTimestamp)
}

func (q *TimeGraph) applyRelationMatrixWithReverse(ctx context.Context, matrix pl.Matrix, relation cmdb.Relation, reverse *cmdb.Relation, targetMatchersByType map[cmdb.Resource]map[string]cmdb.Matcher, targetIDsByTimestamp map[int64]map[cmdb.Resource]map[string]struct{}) (err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-apply-relation-matrix")
	defer finishTimeGraphStage(ctx, span, "apply-relation", time.Now(), &err)
	span.Set("matrix-series-count", len(matrix))
	// 工作缓冲不会被图存储或目标字典持有，可在当前 Matrix 应用后复用。
	info := make(cmdb.Matcher)
	timestamps := make([]int64, 0)
	sourceBuffer, targetBuffer := make(cmdb.Matcher), make(cmdb.Matcher)
	for _, series := range matrix {
		if err := ctx.Err(); err != nil {
			return err
		}
		clear(info)
		for _, label := range series.Metric {
			info[label.Name] = label.Value
		}
		if cap(timestamps) < len(series.Points) {
			timestamps = make([]int64, len(series.Points))
		} else {
			timestamps = timestamps[:len(series.Points)]
		}
		for i, point := range series.Points {
			timestamps[i] = point.T
		}
		if err = q.addTimeRelationWithReverse(ctx, relation, reverse, info, sourceBuffer, targetBuffer, timestamps...); err != nil {
			return errors.WithMessage(err, "add time relation")
		}

		if !timeGraphTargetInfoShow(ctx) {
			continue
		}
		dynamic := relation.Category == string(RelationCategoryDynamic)
		_, targetPrefix := q.relationEndpointPrefixes(relation, relation.V[0], relation.V[1])
		targetInfo := q.relationEndpointInfo(info, relation.V[1], targetPrefix, dynamic)
		if len(targetInfo) == 0 {
			targetInfo = info
		}
		targetType := relation.V[1]
		targetKey := q.primaryMatcherKey(targetType, targetInfo)
		if targetKey == "" {
			continue
		}
		if targetMatchersByType[targetType] == nil {
			targetMatchersByType[targetType] = make(map[string]cmdb.Matcher)
		}
		targetMatchersByType[targetType][targetKey] = q.primaryMatcher(targetType, targetInfo)
		for _, point := range series.Points {
			if targetIDsByTimestamp[point.T] == nil {
				targetIDsByTimestamp[point.T] = make(map[cmdb.Resource]map[string]struct{})
			}
			if targetIDsByTimestamp[point.T][targetType] == nil {
				targetIDsByTimestamp[point.T][targetType] = make(map[string]struct{})
			}
			targetIDsByTimestamp[point.T][targetType][targetKey] = struct{}{}
		}
	}
	return nil
}

func (q *TimeGraph) applySourceMatrix(ctx context.Context, matrix pl.Matrix, sourceType cmdb.Resource) (err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-apply-source-info-matrix")
	defer finishTimeGraphStage(ctx, span, "apply-source-info", time.Now(), &err)
	span.Set("matrix-series-count", len(matrix))
	// 工作缓冲不会被图存储或目标字典持有，可在当前 Matrix 应用后复用。
	info := make(cmdb.Matcher)
	timestamps := make([]int64, 0)
	for _, series := range matrix {
		if err := ctx.Err(); err != nil {
			return err
		}
		clear(info)
		for _, label := range series.Metric {
			info[label.Name] = label.Value
		}
		if cap(timestamps) < len(series.Points) {
			timestamps = make([]int64, len(series.Points))
		} else {
			timestamps = timestamps[:len(series.Points)]
		}
		for i, point := range series.Points {
			timestamps[i] = point.T
		}
		if err = q.AddTimeNode(ctx, sourceType, info, timestamps...); err != nil {
			return errors.WithMessage(err, "add source info node")
		}
		if q.shared != nil && q.shared.sourceInfoBits != nil {
			id, idErr := q.nodeBuilder.GetID(sourceType, info)
			if idErr != nil {
				return idErr
			}
			bits, bitsErr := q.shared.timeBits(timestamps)
			if bitsErr != nil {
				return bitsErr
			}
			q.shared.sourceInfoBits[id] = q.shared.sourceInfoBits[id].union(bits)
		}
	}
	return nil
}

func (q *TimeGraph) applyTargetMatrix(ctx context.Context, matrix pl.Matrix, targetType cmdb.Resource, targetIDsByTimestamp map[int64]map[cmdb.Resource]map[string]struct{}) (err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-apply-target-info-matrix")
	defer finishTimeGraphStage(ctx, span, "apply-target-info", time.Now(), &err)
	span.Set("matrix-series-count", len(matrix))
	// 工作缓冲不会被图存储或目标字典持有，可在当前 Matrix 应用后复用。
	info := make(cmdb.Matcher)
	timestamps := make([]int64, 0)
	for _, series := range matrix {
		if err := ctx.Err(); err != nil {
			return err
		}
		clear(info)
		for _, label := range series.Metric {
			info[label.Name] = label.Value
		}
		timestamps = timestamps[:0]
		key := q.primaryMatcherKey(targetType, info)
		for _, point := range series.Points {
			if _, ok := targetIDsByTimestamp[point.T][targetType][key]; ok {
				timestamps = append(timestamps, point.T)
			}
		}
		if len(timestamps) > 0 {
			if err = q.AddTimeNode(ctx, targetType, info, timestamps...); err != nil {
				return errors.WithMessage(err, "add target info node")
			}
		}
	}
	return nil
}
