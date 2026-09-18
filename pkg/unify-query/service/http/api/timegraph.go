// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type timeGraphQuerier interface {
	QueryPathResources(context.Context, string, string, string, cmdb.Resource, []cmdb.Resource, [][]cmdb.Resource, cmdb.Matcher) ([]cmdb.PathResourcesResult, error)
	QueryPathResourcesRange(context.Context, string, string, string, string, string, cmdb.Resource, []cmdb.Resource, [][]cmdb.Resource, cmdb.Matcher) ([]cmdb.PathResourcesResult, error)
}

func getTimeGraphQuerier(ctx context.Context, spaceUID string) (timeGraphQuerier, error) {
	model, err := v1beta1.GetModel(ctx, spaceUID)
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
	ctx, span := trace.NewSpan(c.Request.Context(), "handler-api-relation-path-resources")
	defer span.End(nil)
	resp := &response{c: c}
	user := metadata.GetUser(ctx)

	request := new(cmdb.RelationPathResourcesRequest)
	if err := json.NewDecoder(c.Request.Body).Decode(request); err != nil {
		resp.failed(ctx, err)
		return
	}
	model, err := getTimeGraphQuerier(ctx, user.SpaceUID)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	data := &cmdb.RelationPathResourcesResponse{
		TraceID: span.TraceID(),
		Data:    make([]cmdb.RelationPathResourcesResponseData, len(request.QueryList)),
	}
	for i, queryItem := range request.QueryList {
		results, queryErr := model.QueryPathResources(ctx, queryItem.LookBackDelta, user.SpaceUID, cast.ToString(queryItem.Timestamp), queryItem.SourceType, queryItem.TargetTypes, queryItem.PathResources, queryItem.Matcher)
		item := cmdb.RelationPathResourcesResponseData{Code: http.StatusOK, Results: results}
		if queryErr != nil {
			item.Code = http.StatusBadRequest
			item.Message = queryErr.Error()
		}
		if item.Results == nil {
			item.Results = make([]cmdb.PathResourcesResult, 0)
		}
		data.Data[i] = item
	}
	resp.success(ctx, data)
}

// HandlerAPIRelationPathResourcesRange queries complete resource paths over a time range.
func HandlerAPIRelationPathResourcesRange(c *gin.Context) {
	ctx, span := trace.NewSpan(c.Request.Context(), "handler-api-relation-path-resources-range")
	defer span.End(nil)
	resp := &response{c: c}
	user := metadata.GetUser(ctx)

	request := new(cmdb.RelationPathResourcesRangeRequest)
	if err := json.NewDecoder(c.Request.Body).Decode(request); err != nil {
		resp.failed(ctx, err)
		return
	}
	model, err := getTimeGraphQuerier(ctx, user.SpaceUID)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	data := &cmdb.RelationPathResourcesRangeResponse{
		TraceID: span.TraceID(),
		Data:    make([]cmdb.RelationPathResourcesRangeResponseData, len(request.QueryList)),
	}
	for i, queryItem := range request.QueryList {
		results, queryErr := model.QueryPathResourcesRange(ctx, queryItem.LookBackDelta, user.SpaceUID, queryItem.Step, cast.ToString(queryItem.StartTs), cast.ToString(queryItem.EndTs), queryItem.SourceType, queryItem.TargetTypes, queryItem.PathResources, queryItem.Matcher)
		item := cmdb.RelationPathResourcesRangeResponseData{Code: http.StatusOK, Results: results}
		if queryErr != nil {
			item.Code = http.StatusBadRequest
			item.Message = queryErr.Error()
		}
		if item.Results == nil {
			item.Results = make([]cmdb.PathResourcesResult, 0)
		}
		data.Data[i] = item
	}
	resp.success(ctx, data)
}
