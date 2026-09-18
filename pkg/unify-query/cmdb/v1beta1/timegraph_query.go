// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta1

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/pkg/errors"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

// buildTimeGraphFromRelations materializes relation metrics into a temporary
// in-memory TimeGraph for one query window.
func (r *model) buildTimeGraphFromRelations(ctx context.Context, spaceUID string, start, end time.Time, step time.Duration, sourceInfo cmdb.Matcher, relations []cmdb.Relation, lookBackDelta string) (*TimeGraph, error) {
	var err error
	ctx, span := trace.NewSpan(ctx, "build-time-graph-from-relations")
	defer span.End(&err)

	tg := NewTimeGraph()
	lookBack, err := time.ParseDuration(lookBackDelta)
	if lookBackDelta != "" && err != nil {
		return nil, errors.WithMessage(err, "parse look back delta")
	}

	queryParams := metadata.GetQueryParams(ctx)
	var instance tsdb.Instance
	if queryParams.IsDirectQuery() {
		instance = prometheus.GetTsDbInstance(ctx, &metadata.Query{
			StorageType: metadata.VictoriaMetricsStorageType,
		})
		if instance == nil {
			return nil, fmt.Errorf("%s storage get error", metadata.VictoriaMetricsStorageType)
		}
	} else {
		instance = prometheus.NewInstance(ctx, promql.GlobalEngine, &prometheus.QueryRangeStorage{
			QueryMaxRouting: QueryMaxRouting,
			Timeout:         Timeout,
		}, lookBack, QueryMaxRouting)
	}

	metadata.GetQueryParams(ctx).SetIsSkipK8s(true)
	instant := start.Equal(end)
	for _, relation := range relations {
		if len(relation.V) != 2 {
			continue
		}

		ctx = metadata.InitHashID(ctx)
		queryTs, err := tg.MakeQueryTs(ctx, spaceUID, sourceInfo, start, end, step, relation)
		if err != nil {
			return nil, errors.WithMessagef(err, "make query ts error for relation %v", relation)
		}
		if queryTs == nil {
			continue
		}

		queryRef, err := queryTs.ToQueryReference(ctx)
		if err != nil {
			return nil, errors.WithMessage(err, "to query reference")
		}
		metadata.SetExpand(ctx, query.ToVmExpand(ctx, queryRef))

		expr, err := queryTs.ToPromExpr(ctx, nil)
		if err != nil {
			return nil, errors.WithMessage(err, "to prom expr")
		}

		var matrix pl.Matrix
		if instant {
			vector, queryErr := instance.DirectQuery(ctx, expr.String(), queryParams.End)
			if queryErr != nil {
				return nil, errors.WithMessage(queryErr, "direct query")
			}
			matrix = vectorToMatrix(vector)
		} else {
			var queryErr error
			matrix, _, queryErr = instance.DirectQueryRange(ctx, expr.String(), queryParams.AlignStart, queryParams.End, queryParams.Step)
			if queryErr != nil {
				return nil, errors.WithMessage(queryErr, "direct query range")
			}
		}

		for _, series := range matrix {
			info := make(cmdb.Matcher, len(series.Metric))
			for _, label := range series.Metric {
				info[label.Name] = label.Value
			}
			timestamps := make([]int64, len(series.Points))
			for i, point := range series.Points {
				timestamps[i] = point.T
			}
			if err = tg.AddTimeRelation(ctx, relation.V[0], relation.V[1], info, timestamps...); err != nil {
				return nil, errors.WithMessage(err, "add time relation")
			}
		}
	}

	return tg, nil
}

// buildRelationsFromPaths extracts and de-duplicates adjacent edges from paths.
func (r *model) buildRelationsFromPaths(paths [][]cmdb.Resource) []cmdb.Relation {
	seen := make(map[[2]cmdb.Resource]struct{})
	for _, path := range paths {
		for i := 0; i+1 < len(path); i++ {
			seen[[2]cmdb.Resource{path[i], path[i+1]}] = struct{}{}
		}
	}

	relations := make([]cmdb.Relation, 0, len(seen))
	for edge := range seen {
		relations = append(relations, cmdb.Relation{V: []cmdb.Resource{edge[0], edge[1]}})
	}
	return relations
}

func (r *model) resolveTimeGraphPaths(ctx context.Context, sourceType cmdb.Resource, targetTypes []cmdb.Resource, requested [][]cmdb.Resource) ([][]cmdb.Resource, error) {
	if len(requested) > 0 {
		paths := make([][]cmdb.Resource, 0, len(requested))
		for _, path := range requested {
			if len(path) >= 2 {
				paths = append(paths, path)
			}
		}
		if len(paths) > 0 {
			return paths, nil
		}
	}

	var paths [][]cmdb.Resource
	for _, targetType := range targetTypes {
		graphPaths, err := r.getPaths(ctx, sourceType, targetType, nil)
		if err != nil {
			continue
		}
		for _, graphPath := range graphPaths {
			path := make([]cmdb.Resource, 0, len(graphPath))
			for _, resource := range graphPath {
				path = append(path, cmdb.Resource(resource))
			}
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return nil, errors.New("no paths found")
	}
	return paths, nil
}

func (r *model) queryTimeGraph(ctx context.Context, lookBackDelta, spaceUID string, start, end time.Time, step time.Duration, sourceType cmdb.Resource, targetTypes []cmdb.Resource, pathResources [][]cmdb.Resource, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	paths, err := r.resolveTimeGraphPaths(ctx, sourceType, targetTypes, pathResources)
	if err != nil {
		return nil, err
	}

	tg, err := r.buildTimeGraphFromRelations(ctx, spaceUID, start, end, step, matcher, r.buildRelationsFromPaths(paths), lookBackDelta)
	if err != nil {
		return nil, errors.WithMessage(err, "build time graph")
	}
	defer tg.Clean(ctx)

	results := make([]cmdb.PathResourcesResult, 0)
	for _, targetType := range targetTypes {
		pathResults, queryErr := tg.FindShortestPath(ctx, sourceType, targetType, matcher)
		if queryErr != nil {
			return nil, errors.WithMessagef(queryErr, "find path %s => %s", sourceType, targetType)
		}
		for _, result := range pathResults {
			results = append(results, cmdb.PathResourcesResult{
				Timestamp:  result.Timestamp,
				TargetType: result.TargetType,
				Path:       result.Path,
			})
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Timestamp == results[j].Timestamp {
			return results[i].TargetType < results[j].TargetType
		}
		return results[i].Timestamp < results[j].Timestamp
	})
	return results, nil
}

func (r *model) QueryPathResources(ctx context.Context, lookBackDelta, spaceUID, timestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, pathResources [][]cmdb.Resource, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	if spaceUID == "" {
		return nil, errors.New("space uid is empty")
	}
	if timestamp == "" {
		return nil, errors.New("timestamp is empty")
	}
	if sourceType == "" {
		return nil, errors.New("source type is empty")
	}
	if len(targetTypes) == 0 {
		return nil, errors.New("target types is empty")
	}
	timestampValue, err := cast.ToInt64E(timestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse timestamp")
	}
	return r.queryTimeGraph(ctx, lookBackDelta, spaceUID, time.Unix(timestampValue, 0), time.Unix(timestampValue, 0), 5*time.Minute, sourceType, targetTypes, pathResources, matcher)
}

func (r *model) QueryPathResourcesRange(ctx context.Context, lookBackDelta, spaceUID, step, startTimestamp, endTimestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, pathResources [][]cmdb.Resource, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	if spaceUID == "" {
		return nil, errors.New("space uid is empty")
	}
	if startTimestamp == "" || endTimestamp == "" {
		return nil, errors.New("timestamp is empty")
	}
	if sourceType == "" {
		return nil, errors.New("source type is empty")
	}
	if len(targetTypes) == 0 {
		return nil, errors.New("target types is empty")
	}
	start, err := cast.ToInt64E(startTimestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse start timestamp")
	}
	end, err := cast.ToInt64E(endTimestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse end timestamp")
	}
	stepDuration, err := time.ParseDuration(step)
	if err != nil {
		return nil, errors.WithMessage(err, "parse step")
	}
	if stepDuration <= 0 {
		return nil, errors.New("step must be positive")
	}
	return r.queryTimeGraph(ctx, lookBackDelta, spaceUID, time.Unix(start, 0), time.Unix(end, 0), stepDuration, sourceType, targetTypes, pathResources, matcher)
}
