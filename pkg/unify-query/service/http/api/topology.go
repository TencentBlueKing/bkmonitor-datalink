// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
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
	ctx, span := trace.NewSpan(c.Request.Context(), "handler-api-relation-v1beta3-topology")
	var handlerErr error
	defer span.End(&handlerErr)
	resp := &response{c: c}

	request := new(cmdb.SharedTopologyRequest)
	if err := json.NewDecoder(c.Request.Body).Decode(request); err != nil {
		handlerErr = err
		resp.failed(ctx, err)
		return
	}
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
	for index, query := range request.QueryList {
		queryCtx, querySpan := trace.NewSpan(ctx, "handler-api-relation-v1beta3-topology-item")
		query.SpaceUID = metadata.GetUser(ctx).SpaceUID
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
		}
		if queryErr != nil {
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
		querySpan.End(&queryErr)
		data.Data[index] = item
	}
	span.Set("query-count", len(request.QueryList))
	resp.success(ctx, data)
}
