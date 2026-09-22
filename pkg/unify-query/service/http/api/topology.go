// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// HandlerAPIRelationV1Beta3Topology 查询单个时间点的完整局部拓扑。
// 该接口与旧 path/multi_resource 接口分离，返回每个快照的节点和关系。
// @Summary  query relation shared topology
// @ID       relation_shared_topology_query_v1beta3
// @Produce  json
// @Param    traceparent            header    string                         false  "TraceID"
// @Param    X-Bk-Scope-Space-Uid   header    string                         false  "空间UID" default(bkcc__2)
// @Param    data                  body      cmdb.SharedTopologyRequest      true   "json data"
// @Success  200                   {object}  cmdb.SharedTopologyResponse
// @Failure  400                   {object}  ErrResponse
// @Router   /api/v1/relation/v1beta3/topology [post]
func HandlerAPIRelationV1Beta3Topology(c *gin.Context) {
	handleAPIRelationV1Beta3Topology(c, false)
}

// HandlerAPIRelationV1Beta3TopologyRange 查询统一时间网格上的完整局部拓扑。
// @Summary  query relation shared topology range
// @ID       relation_shared_topology_query_range_v1beta3
// @Produce  json
// @Param    traceparent            header    string                         false  "TraceID"
// @Param    X-Bk-Scope-Space-Uid   header    string                         false  "空间UID" default(bkcc__2)
// @Param    data                  body      cmdb.SharedTopologyRequest      true   "json data"
// @Success  200                   {object}  cmdb.SharedTopologyResponse
// @Failure  400                   {object}  ErrResponse
// @Router   /api/v1/relation/v1beta3/topology_range [post]
func HandlerAPIRelationV1Beta3TopologyRange(c *gin.Context) {
	handleAPIRelationV1Beta3Topology(c, true)
}

func handleAPIRelationV1Beta3Topology(c *gin.Context, rangeQuery bool) {
	spanName := "handler-api-relation-v1beta3-topology"
	if rangeQuery {
		spanName += "-range"
	}
	ctx, span := trace.NewSpan(c.Request.Context(), spanName)
	var handlerErr error
	defer span.End(&handlerErr)
	resp := &response{c: c}
	queryMode := metric.CMDBRelationQueryModeInstant
	if rangeQuery {
		queryMode = metric.CMDBRelationQueryModeRange
	}
	started := time.Now()
	requestResult := metric.CMDBRelationResultSuccess
	defer func() {
		if handlerErr != nil && requestResult != metric.CMDBRelationResultRejected {
			requestResult = metric.CMDBTimeGraphErrorResult(handlerErr)
		}
		metric.CMDBTopologyObserve(ctx, metric.CMDBTopologyScopeRequest, queryMode, requestResult, time.Since(started))
	}()
	span.Set("query-mode", queryMode)
	span.Set("handler-headers", c.Request.Header)

	request := new(cmdb.SharedTopologyRequest)
	if err := json.NewDecoder(c.Request.Body).Decode(request); err != nil {
		handlerErr = err
		requestResult = metric.CMDBRelationResultRejected
		resp.failed(ctx, err)
		return
	}
	span.Set("query-count", len(request.QueryList))
	metric.CMDBRelationQueryListSizeObserve(ctx, metric.CMDBRelationRouteTimeGraph, queryMode, len(request.QueryList))
	model, err := v1beta3.GetModel(ctx)
	if err != nil {
		handlerErr = err
		resp.failed(ctx, err)
		return
	}
	topologyModel, ok := model.(cmdb.SharedTopologyCMDB)
	if !ok {
		err = fmt.Errorf("relation model does not support shared topology query")
		handlerErr = err
		resp.failed(ctx, err)
		return
	}

	data := &cmdb.SharedTopologyResponse{
		TraceID: span.TraceID(),
		Data:    make([]cmdb.SharedTopologyResponseData, len(request.QueryList)),
	}
	failedQueryCount := 0
	partialQueryCount := 0
	itemSpanName := "handler-api-relation-v1beta3-topology-item"
	if rangeQuery {
		itemSpanName += "-range"
	}
	for index, query := range request.QueryList {
		queryStarted := time.Now()
		queryCtx, querySpan := trace.NewSpan(ctx, itemSpanName)
		query.SpaceUID = metadata.GetUser(ctx).SpaceUID
		querySpan.Set("query-index", index)
		querySpan.Set("requested-source-type", query.SourceType)
		querySpan.Set("requested-target-types", query.TargetTypes)
		querySpan.Set("requested-max-hops", query.MaxHops)
		querySpan.Set("requested-source-matcher-count", len(query.SourceInfo))
		querySpan.Set("requested-relation-type-count", len(query.AllowedRelationTypes))
		querySpan.Set("requested-category-count", len(query.AllowedCategories))
		querySpan.Set("requested-timestamp", query.Timestamp)
		querySpan.Set("requested-start", query.StartTime)
		querySpan.Set("requested-end", query.EndTime)
		querySpan.Set("requested-step", query.Step)
		item := cmdb.SharedTopologyResponseData{Code: http.StatusOK}
		var result cmdb.SharedTopologyResult
		var queryErr error
		if rangeQuery {
			if query.Timestamp != 0 {
				queryErr = fmt.Errorf("range topology query does not accept timestamp")
			} else if query.StartTime == 0 || query.EndTime == 0 {
				queryErr = fmt.Errorf("range topology query requires start_time and end_time")
			}
		} else if query.StartTime != 0 || query.EndTime != 0 {
			queryErr = fmt.Errorf("instant topology query does not accept start_time or end_time")
		} else if query.Timestamp == 0 {
			queryErr = fmt.Errorf("instant topology query requires timestamp")
		}
		if queryErr == nil {
			result, queryErr = topologyModel.QuerySharedTopology(queryCtx, query)
		} else {
			// HTTP 时间模式校验未进入模型，单独记录该子查询，避免遗漏或重复计数。
			metric.CMDBTopologyObserve(queryCtx, metric.CMDBTopologyScopeQuery, queryMode, metric.CMDBRelationResultRejected, time.Since(queryStarted))
			metric.CMDBTopologyRejectInc(queryCtx, queryMode, "invalid_request")
		}
		if queryErr != nil {
			failedQueryCount++
			item.Code = http.StatusBadRequest
			item.Message = queryErr.Error()
		} else {
			item.StartTime = result.StartTime
			item.EndTime = result.EndTime
			item.Step = result.Step
			item.PointCount = result.PointCount
			item.Snapshots = result.Snapshots
		}
		if item.Snapshots == nil {
			item.Snapshots = make([]cmdb.SharedTopologySnapshot, 0)
		}
		querySpan.Set("query-index", index)
		querySpan.Set("point-count", item.PointCount)
		querySpan.Set("response-code", item.Code)
		querySpan.Set("snapshot-count", len(item.Snapshots))
		nodeCount, edgeCount, partialCount := 0, 0, 0
		for _, snapshot := range item.Snapshots {
			nodeCount += len(snapshot.Nodes)
			edgeCount += len(snapshot.Edges)
			if snapshot.Partial {
				partialCount++
			}
		}
		querySpan.Set("response-node-count", nodeCount)
		querySpan.Set("response-edge-count", edgeCount)
		querySpan.Set("partial-snapshot-count", partialCount)
		if partialCount > 0 {
			partialQueryCount++
		}
		querySpan.End(&queryErr)
		data.Data[index] = item
	}
	span.Set("query-count", len(request.QueryList))
	span.Set("failed-query-count", failedQueryCount)
	span.Set("successful-query-count", len(request.QueryList)-failedQueryCount)
	span.Set("partial-failure", failedQueryCount > 0)
	switch {
	case len(request.QueryList) == 0:
		requestResult = metric.CMDBRelationResultEmpty
	case failedQueryCount == len(request.QueryList):
		requestResult = metric.CMDBRelationResultFailed
	case failedQueryCount > 0 || partialQueryCount > 0:
		requestResult = metric.CMDBRelationResultPartial
	}
	resp.success(ctx, data)
}
