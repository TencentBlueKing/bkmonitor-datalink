// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type timeGraphQuerier interface {
	QueryPathResources(context.Context, string, string, string, cmdb.Resource, []cmdb.Resource, [][]cmdb.Resource, cmdb.Matcher) ([]cmdb.PathResourcesResult, error)
	QueryPathResourcesRange(context.Context, string, string, string, string, string, cmdb.Resource, []cmdb.Resource, [][]cmdb.Resource, cmdb.Matcher) ([]cmdb.PathResourcesResult, error)
}

func observeTimeGraphPathQueryMetrics(ctx context.Context, queryMode string, started time.Time, results []cmdb.PathResourcesResult, queryErr error) {
	result := metric.CMDBRelationResultSuccess
	if queryErr != nil {
		result = metric.CMDBRelationResultFailed
	} else if len(results) == 0 {
		result = metric.CMDBRelationResultEmpty
	}
	metric.CMDBRelationRouteInc(ctx, metric.CMDBRelationRouteTimeGraph, queryMode, result)
	metric.CMDBRelationRouteSecond(ctx, time.Since(started), metric.CMDBRelationRouteTimeGraph, queryMode)
	metric.CMDBRelationPathResultInc(ctx, metric.CMDBRelationRouteTimeGraph, queryMode, result)
	metric.CMDBRelationTimeGraphResultCountObserve(ctx, queryMode, len(results))
	if queryMode == metric.CMDBRelationQueryModeRange {
		buckets := make(map[int64]struct{}, len(results))
		for _, item := range results {
			buckets[item.Timestamp] = struct{}{}
		}
		metric.CMDBRelationTimeGraphBucketCountObserve(ctx, queryMode, len(buckets))
	}
}

func getTimeGraphQuerier(ctx context.Context, spaceUID string) (timeGraphQuerier, error) {
	model, err := v1beta3.GetModel(ctx)
	if err != nil {
		return nil, err
	}
	querier, ok := model.(timeGraphQuerier)
	if !ok {
		return nil, fmt.Errorf("relation model does not support TimeGraph query")
	}
	return querier, nil
}

// HandlerAPIRelationPathResources queries the complete resource path at one timestamp.
func HandlerAPIRelationPathResources(c *gin.Context) {
	var err error
	ctx, span := trace.NewSpan(c.Request.Context(), "handler-api-relation-path-resources")
	defer span.End(&err)
	resp := &response{c: c}
	user := metadata.GetUser(ctx)

	request := new(cmdb.RelationPathResourcesRequest)
	if err = json.NewDecoder(c.Request.Body).Decode(request); err != nil {
		resp.failed(ctx, err)
		return
	}
	span.Set("query-mode", metric.CMDBRelationQueryModeInstant)
	span.Set("query-count", len(request.QueryList))
	metric.CMDBRelationQueryListSizeObserve(ctx, metric.CMDBRelationRouteTimeGraph, metric.CMDBRelationQueryModeInstant, len(request.QueryList))
	model, err := getTimeGraphQuerier(ctx, user.SpaceUID)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	data := &cmdb.RelationPathResourcesResponse{
		TraceID: span.TraceID(),
		Data:    make([]cmdb.RelationPathResourcesResponseData, len(request.QueryList)),
	}
	failedQueryCount := 0
	for i, queryItem := range request.QueryList {
		queryCtx, querySpan := trace.NewSpan(ctx, "handler-api-relation-path-resources-item")
		querySpan.Set("query-index", i)
		querySpan.Set("requested-source-type", queryItem.SourceType)
		querySpan.Set("requested-target-types", queryItem.TargetTypes)
		querySpan.Set("requested-path-count", len(queryItem.PathResources))
		querySpan.Set("requested-matcher-count", len(queryItem.Matcher))
		queryStarted := time.Now()
		metric.CMDBRelationRouteInc(queryCtx, metric.CMDBRelationRouteTimeGraph, metric.CMDBRelationQueryModeInstant, metric.CMDBRelationResultStarted)
		results, queryErr := model.QueryPathResources(queryCtx, queryItem.LookBackDelta, user.SpaceUID, cast.ToString(queryItem.Timestamp), queryItem.SourceType, queryItem.TargetTypes, queryItem.PathResources, queryItem.Matcher)
		observeTimeGraphPathQueryMetrics(queryCtx, metric.CMDBRelationQueryModeInstant, queryStarted, results, queryErr)
		querySpan.Set("result-count", len(results))
		querySpan.End(&queryErr)
		item := cmdb.RelationPathResourcesResponseData{Code: http.StatusOK, Results: results}
		if queryErr != nil {
			failedQueryCount++
			item.Code = http.StatusBadRequest
			item.Message = queryErr.Error()
		}
		if item.Results == nil {
			item.Results = make([]cmdb.PathResourcesResult, 0)
		}
		data.Data[i] = item
	}
	span.Set("failed-query-count", failedQueryCount)
	span.Set("successful-query-count", len(request.QueryList)-failedQueryCount)
	span.Set("partial-failure", failedQueryCount > 0)
	resp.success(ctx, data)
}

// HandlerAPIRelationPathResourcesRange queries complete resource paths over a time range.
func HandlerAPIRelationPathResourcesRange(c *gin.Context) {
	var err error
	ctx, span := trace.NewSpan(c.Request.Context(), "handler-api-relation-path-resources-range")
	defer span.End(&err)
	resp := &response{c: c}
	user := metadata.GetUser(ctx)

	request := new(cmdb.RelationPathResourcesRangeRequest)
	if err = json.NewDecoder(c.Request.Body).Decode(request); err != nil {
		resp.failed(ctx, err)
		return
	}
	span.Set("query-mode", metric.CMDBRelationQueryModeRange)
	span.Set("query-count", len(request.QueryList))
	metric.CMDBRelationQueryListSizeObserve(ctx, metric.CMDBRelationRouteTimeGraph, metric.CMDBRelationQueryModeRange, len(request.QueryList))
	model, err := getTimeGraphQuerier(ctx, user.SpaceUID)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	data := &cmdb.RelationPathResourcesRangeResponse{
		TraceID: span.TraceID(),
		Data:    make([]cmdb.RelationPathResourcesRangeResponseData, len(request.QueryList)),
	}
	failedQueryCount := 0
	for i, queryItem := range request.QueryList {
		queryCtx, querySpan := trace.NewSpan(ctx, "handler-api-relation-path-resources-range-item")
		querySpan.Set("query-index", i)
		querySpan.Set("requested-source-type", queryItem.SourceType)
		querySpan.Set("requested-target-types", queryItem.TargetTypes)
		querySpan.Set("requested-path-count", len(queryItem.PathResources))
		querySpan.Set("requested-matcher-count", len(queryItem.Matcher))
		querySpan.Set("requested-step", queryItem.Step)
		queryStarted := time.Now()
		metric.CMDBRelationRouteInc(queryCtx, metric.CMDBRelationRouteTimeGraph, metric.CMDBRelationQueryModeRange, metric.CMDBRelationResultStarted)
		results, queryErr := model.QueryPathResourcesRange(queryCtx, queryItem.LookBackDelta, user.SpaceUID, queryItem.Step, cast.ToString(queryItem.StartTs), cast.ToString(queryItem.EndTs), queryItem.SourceType, queryItem.TargetTypes, queryItem.PathResources, queryItem.Matcher)
		observeTimeGraphPathQueryMetrics(queryCtx, metric.CMDBRelationQueryModeRange, queryStarted, results, queryErr)
		querySpan.Set("result-count", len(results))
		querySpan.End(&queryErr)
		item := cmdb.RelationPathResourcesRangeResponseData{Code: http.StatusOK, Results: results}
		if queryErr != nil {
			failedQueryCount++
			item.Code = http.StatusBadRequest
			item.Message = queryErr.Error()
		}
		if item.Results == nil {
			item.Results = make([]cmdb.PathResourcesResult, 0)
		}
		data.Data[i] = item
	}
	span.Set("failed-query-count", failedQueryCount)
	span.Set("successful-query-count", len(request.QueryList)-failedQueryCount)
	span.Set("partial-failure", failedQueryCount > 0)
	resp.success(ctx, data)
}
