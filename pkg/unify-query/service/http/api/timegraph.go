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

func observeTimeGraphPathQueryMetrics(ctx context.Context, queryMode string, started time.Time, results []cmdb.PathResourcesResult, queryErr error) {
	result := metric.CMDBRelationResultSuccess
	if queryErr != nil {
		result = metric.CMDBRelationResultFailed
	} else if status := metadata.GetStatus(ctx); status != nil && status.Code == metadata.QueryTsPartial {
		result = metric.CMDBRelationResultPartial
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

func getTimeGraphQuerier(ctx context.Context, spaceUID string) (querier *v1beta3.Model, err error) {
	ctx, span := trace.NewSpan(ctx, "handler-api-timegraph-resolve-querier")
	defer span.End(&err)
	span.Set("space-uid", spaceUID)
	querier, err = v1beta3.GetModel(ctx)
	if err != nil {
		return nil, err
	}
	span.Set("querier-type", fmt.Sprintf("%T", querier))
	return querier, nil
}

// HandlerAPIRelationPathResources queries the complete resource path at one timestamp.
// @Summary  query complete relation resource paths
// @ID       relation_path_resources_query
// @Description Direct relation endpoint. Send X-Bk-Scope-Space-Uid for routed queries; it has no built-in default. JWT authorization is conditional on server configuration. HTTP 200 still requires checking each data item code. See docs/api/timegraph.md for time, empty/partial, and migration contracts.
// @Accept json
// @Produce  json
// @Param    traceparent           header    string                       false  "TraceID"
// @Param X-Bkapi-Jwt header string false "Gateway-signed JWT; required when jwt.enabled is true. BasicAuth is not enforced by these relation handlers."
// @Param X-Bk-Scope-Space-Uid header string false "Routing space UID; no default. Required for physical routing and when configured JWT space authorization requires it."
// @Param    data                  body      cmdb.RelationPathResourcesRequest       true   "json data"
// @Success  200                   {object}  cmdb.RelationPathResourcesResponse
// @Failure 401 {object} ErrResponse "JWT authorization failed when enabled"
// @Failure  400                   {object}  ErrResponse
// @Router   /api/v1/relation/path_resources [post]
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
		queryCtx, querySpan := trace.NewSpan(metadata.WithStatusScope(ctx, i), "handler-api-relation-path-resources-item")
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
		if status := metadata.GetStatus(queryCtx); queryErr == nil && status != nil && status.Code == metadata.QueryTsPartial {
			item.Partial, item.PartialReason = true, status.Message
		}
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
// @Summary  query complete relation resource paths over a time range
// @ID       relation_path_resources_query_range
// @Description Direct relation endpoint. Send X-Bk-Scope-Space-Uid for routed queries; it has no built-in default. JWT authorization is conditional on server configuration. HTTP 200 still requires checking each data item code. See docs/api/timegraph.md for time, empty/partial, and migration contracts.
// @Accept json
// @Produce  json
// @Param    traceparent           header    string                       false  "TraceID"
// @Param X-Bkapi-Jwt header string false "Gateway-signed JWT; required when jwt.enabled is true. BasicAuth is not enforced by these relation handlers."
// @Param X-Bk-Scope-Space-Uid header string false "Routing space UID; no default. Required for physical routing and when configured JWT space authorization requires it."
// @Param    data                  body      cmdb.RelationPathResourcesRangeRequest  true   "json data"
// @Success  200                   {object}  cmdb.RelationPathResourcesRangeResponse
// @Failure 401 {object} ErrResponse "JWT authorization failed when enabled"
// @Failure  400                   {object}  ErrResponse
// @Router   /api/v1/relation/path_resources_range [post]
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
		queryCtx, querySpan := trace.NewSpan(metadata.WithStatusScope(ctx, i), "handler-api-relation-path-resources-range-item")
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
		if status := metadata.GetStatus(queryCtx); queryErr == nil && status != nil && status.Code == metadata.QueryTsPartial {
			item.Partial, item.PartialReason = true, status.Message
		}
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
