// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	ants "github.com/panjf2000/ants/v2"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// HandlerAPIRelationMultiResource
// @Summary  query relation multi resource
// @ID       relation_multi_resource_query
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    X-Bk-Scope-Space-Uid   header    string                          false  "空间UID" default(bkcc__2)
// @Param    data                  	body      cmdb.RelationMultiResourceRequest			  true   "json data"
// @Success  200                   	{object}  cmdb.RelationMultiResourceResponse
// @Failure  400                   	{object}  ErrResponse
// @Router   /api/v1/relation/multi_resource [post]
func HandlerAPIRelationMultiResource(c *gin.Context) {
	var (
		ctx = c.Request.Context()

		user = metadata.GetUser(ctx)
		err  error

		resp = &response{
			c: c,
		}
	)

	ctx, span := trace.NewSpan(ctx, "handler-api-relation-multi-resource")
	defer span.End(&err)

	request := new(cmdb.RelationMultiResourceRequest)
	err = json.NewDecoder(c.Request.Body).Decode(request)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	paramsBody, _ := json.Marshal(request)
	span.Set("handler-headers", c.Request.Header)
	span.Set("handler-body", string(paramsBody))

	model, err := v1beta1.GetModel(ctx, user.SpaceUID)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	data := new(cmdb.RelationMultiResourceResponse)
	data.TraceID = span.TraceID()
	data.Data = make([]cmdb.RelationMultiResourceResponseData, len(request.QueryList))
	allPathQueryCount := 0
	for _, qry := range request.QueryList {
		if qry.ReturnAllPaths {
			allPathQueryCount++
		}
	}
	metric.CMDBRelationQueryListSizeObserve(ctx, "vm_legacy", metric.CMDBRelationQueryModeInstant, len(request.QueryList))
	span.Set("query-mode", metric.CMDBRelationQueryModeInstant)
	span.Set("query-count", len(request.QueryList))
	span.Set("all-path-query-count", allPathQueryCount)

	var (
		sendWg           sync.WaitGroup
		lock             sync.Mutex
		failedQueryCount int
	)
	p, err := ants.NewPool(RelationMaxRouting)
	if err != nil {
		resp.failed(ctx, fmt.Errorf("create relation worker pool: %w", err))
		return
	}
	defer p.Release()

	for idx, qry := range request.QueryList {
		idx := idx
		qry := qry
		sendWg.Add(1)
		if submitErr := p.Submit(func() {
			defer sendWg.Done()
			queryCtx, querySpan := trace.NewSpan(ctx, "handler-api-relation-multi-resource-item")
			var queryErr error
			defer querySpan.End(&queryErr)
			querySpan.Set("query-index", idx)
			querySpan.Set("return-all-paths", qry.ReturnAllPaths)
			querySpan.Set("requested-source-type", string(qry.SourceType))
			querySpan.Set("requested-target-type", string(qry.TargetType))
			d := cmdb.RelationMultiResourceResponseData{
				Code: http.StatusOK,
			}

			timestamp := cast.ToString(qry.Timestamp)
			if qry.ReturnAllPaths {
				multiPathModel, ok := model.(cmdb.MultiPathCMDB)
				if !ok {
					queryErr = fmt.Errorf("relation model does not support multi-path query")
				} else {
					var paths []cmdb.RelationMultiResourcePathData
					d.SourceType, d.SourceInfo, paths, d.TargetType, queryErr = multiPathModel.QueryResourceMatcherAll(queryCtx, qry.LookBackDelta, user.SpaceUID, timestamp, qry.TargetType, qry.SourceType, qry.SourceInfo, qry.SourceExpandInfo, qry.TargetInfoShow, qry.PathResource)
					d.Paths = paths
					d.Path, d.TargetList = selectLegacyInstantPath(paths)
				}
			} else {
				d.SourceType, d.SourceInfo, d.Path, d.TargetType, d.TargetList, queryErr = model.QueryResourceMatcher(queryCtx, qry.LookBackDelta, user.SpaceUID, timestamp, qry.TargetType, qry.SourceType, qry.SourceInfo, qry.SourceExpandInfo, qry.TargetInfoShow, qry.PathResource)
			}
			if queryErr != nil {
				d.Message = queryErr.Error()
				d.Code = http.StatusBadRequest
			}

			// 返回给到 saas 的数据，不能为 null，必须要是 []，否则会报错
			if d.TargetList == nil {
				d.TargetList = make(cmdb.Matchers, 0)
			}
			pathCount := len(d.Paths)
			if pathCount == 0 && len(d.Path) > 0 {
				pathCount = 1
			}
			querySpan.Set("executed-path-count", pathCount)
			querySpan.Set("target-count", len(d.TargetList))
			querySpan.Set("response-code", d.Code)

			lock.Lock()
			data.Data[idx] = d
			if queryErr != nil {
				failedQueryCount++
			}
			lock.Unlock()
		}); submitErr != nil {
			sendWg.Done()
			lock.Lock()
			data.Data[idx] = cmdb.RelationMultiResourceResponseData{
				Code:       http.StatusBadRequest,
				Message:    submitErr.Error(),
				TargetList: make(cmdb.Matchers, 0),
			}
			failedQueryCount++
			lock.Unlock()
		}
	}
	sendWg.Wait()
	span.Set("failed-query-count", failedQueryCount)
	span.Set("successful-query-count", len(request.QueryList)-failedQueryCount)
	span.Set("partial-failure", failedQueryCount > 0)

	resp.success(ctx, data)
}

// HandlerAPIRelationMultiResourceRange
// @Summary  query relation multi resource
// @ID       relation_multi_resource_query_range
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    X-Bk-Scope-Space-Uid   header    string                          false  "空间UID" default(bkcc__2)
// @Param    data                  	body      cmdb.RelationMultiResourceRangeRequest			  true   "json data"
// @Success  200                   	{object}  cmdb.RelationMultiResourceRangeResponse
// @Failure  400                   	{object}  ErrResponse
// @Router   /api/v1/relation/multi_resource_range [post]
func HandlerAPIRelationMultiResourceRange(c *gin.Context) {
	var (
		ctx = c.Request.Context()

		user = metadata.GetUser(ctx)
		err  error

		resp = &response{
			c: c,
		}
	)

	ctx, span := trace.NewSpan(ctx, "handler-api-relation-multi-resource-range")
	defer span.End(&err)

	request := new(cmdb.RelationMultiResourceRangeRequest)
	err = json.NewDecoder(c.Request.Body).Decode(request)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	paramsBody, _ := json.Marshal(request)
	span.Set("handler-headers", c.Request.Header)
	span.Set("handler-body", string(paramsBody))

	model, err := v1beta1.GetModel(ctx, user.SpaceUID)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	data := new(cmdb.RelationMultiResourceRangeResponse)
	data.TraceID = span.TraceID()
	data.Data = make([]cmdb.RelationMultiResourceRangeResponseData, len(request.QueryList))
	allPathQueryCount := 0
	for _, qry := range request.QueryList {
		if qry.ReturnAllPaths {
			allPathQueryCount++
		}
	}
	metric.CMDBRelationQueryListSizeObserve(ctx, "vm_legacy", metric.CMDBRelationQueryModeRange, len(request.QueryList))
	span.Set("query-mode", metric.CMDBRelationQueryModeRange)
	span.Set("query-count", len(request.QueryList))
	span.Set("all-path-query-count", allPathQueryCount)

	var (
		sendWg           sync.WaitGroup
		lock             sync.Mutex
		failedQueryCount int
	)
	p, err := ants.NewPool(RelationMaxRouting)
	if err != nil {
		resp.failed(ctx, fmt.Errorf("create relation range worker pool: %w", err))
		return
	}
	defer p.Release()

	for idx, qry := range request.QueryList {
		idx := idx
		qry := qry
		sendWg.Add(1)
		if submitErr := p.Submit(func() {
			defer sendWg.Done()
			queryCtx, querySpan := trace.NewSpan(ctx, "handler-api-relation-multi-resource-range-item")
			var queryErr error
			defer querySpan.End(&queryErr)
			querySpan.Set("query-index", idx)
			querySpan.Set("return-all-paths", qry.ReturnAllPaths)
			querySpan.Set("requested-source-type", string(qry.SourceType))
			querySpan.Set("requested-target-type", string(qry.TargetType))
			d := cmdb.RelationMultiResourceRangeResponseData{
				Code: http.StatusOK,
			}

			startTs := cast.ToString(qry.StartTs)
			endTs := cast.ToString(qry.EndTs)
			if qry.ReturnAllPaths {
				multiPathModel, ok := model.(cmdb.MultiPathCMDB)
				if !ok {
					queryErr = fmt.Errorf("relation model does not support multi-path range query")
				} else {
					var paths []cmdb.RelationMultiResourceRangePathData
					d.SourceType, d.SourceInfo, paths, d.TargetType, queryErr = multiPathModel.QueryResourceMatcherRangeAll(queryCtx, qry.LookBackDelta, user.SpaceUID, qry.Step, startTs, endTs, qry.TargetType, qry.SourceType, qry.SourceInfo, qry.SourceExpandInfo, qry.TargetInfoShow, qry.PathResource)
					d.Paths = paths
					d.Path, d.TargetList = selectLegacyRangePath(paths)
				}
			} else {
				d.SourceType, d.SourceInfo, d.Path, d.TargetType, d.TargetList, queryErr = model.QueryResourceMatcherRange(queryCtx, qry.LookBackDelta, user.SpaceUID, qry.Step, startTs, endTs, qry.TargetType, qry.SourceType, qry.SourceInfo, qry.SourceExpandInfo, qry.TargetInfoShow, qry.PathResource)
			}
			if queryErr != nil {
				d.Message = metadata.NewMessage(
					metadata.MsgQueryRelation,
					"关联数据查询异常",
				).Error(ctx, queryErr).Error()
				d.Code = http.StatusBadRequest
			}

			if len(d.Path) > 0 {
				d.SourceType = cmdb.Resource(d.Path[0])
				d.TargetType = cmdb.Resource(d.Path[len(d.Path)-1])
			}

			// 返回给到 saas 的数据，不能为 null，必须要是 []，否则会报错
			if d.TargetList == nil {
				d.TargetList = make([]cmdb.MatchersWithTimestamp, 0)
			}
			pathCount := len(d.Paths)
			if pathCount == 0 && len(d.Path) > 0 {
				pathCount = 1
			}
			targetCount := 0
			for _, bucket := range d.TargetList {
				targetCount += len(bucket.Matchers)
			}
			querySpan.Set("executed-path-count", pathCount)
			querySpan.Set("target-count", targetCount)
			querySpan.Set("response-code", d.Code)

			lock.Lock()
			data.Data[idx] = d
			if queryErr != nil {
				failedQueryCount++
			}
			lock.Unlock()
		}); submitErr != nil {
			sendWg.Done()
			lock.Lock()
			data.Data[idx] = cmdb.RelationMultiResourceRangeResponseData{
				Code:       http.StatusBadRequest,
				Message:    submitErr.Error(),
				TargetList: make([]cmdb.MatchersWithTimestamp, 0),
			}
			failedQueryCount++
			lock.Unlock()
		}
	}
	sendWg.Wait()
	span.Set("failed-query-count", failedQueryCount)
	span.Set("successful-query-count", len(request.QueryList)-failedQueryCount)
	span.Set("partial-failure", failedQueryCount > 0)

	resp.success(ctx, data)
}

// HandlerAPIRelationV1Beta3MultiResource
// @Summary  query relation multi resource (v1beta3, TimeGraph)
// @ID       relation_multi_resource_query_v1beta3
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID"
// @Param    X-Bk-Scope-Space-Uid   header    string                          false  "空间UID" default(bkcc__2)
// @Param    data                  	body      cmdb.RelationMultiResourceRequest			  true   "json data"
// @Success  200                   	{object}  cmdb.RelationMultiResourceResponse
// @Failure  400                   	{object}  ErrResponse
// @Router   /api/v1/relation/v1beta3/multi_resource [post]
func HandlerAPIRelationV1Beta3MultiResource(c *gin.Context) {
	var (
		ctx = c.Request.Context()

		user = metadata.GetUser(ctx)
		err  error

		resp = &response{
			c: c,
		}
	)

	ctx, span := trace.NewSpan(ctx, "handler-api-relation-multi-resource-v1beta3")
	defer span.End(&err)

	request := new(cmdb.RelationMultiResourceRequest)
	err = json.NewDecoder(c.Request.Body).Decode(request)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	paramsBody, _ := json.Marshal(request)
	span.Set("handler-headers", c.Request.Header)
	span.Set("handler-body", string(paramsBody))
	span.Set("query-mode", metric.CMDBRelationQueryModeInstant)
	metric.CMDBRelationQueryListSizeObserve(ctx, metric.CMDBRelationRouteTimeGraph, metric.CMDBRelationQueryModeInstant, len(request.QueryList))

	model, err := v1beta3.GetModel(ctx)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	data := new(cmdb.RelationMultiResourceResponse)
	data.TraceID = span.TraceID()
	data.Data = make([]cmdb.RelationMultiResourceResponseData, len(request.QueryList))

	var (
		sendWg           sync.WaitGroup
		lock             sync.Mutex
		failedQueryCount int
	)
	p, err := ants.NewPool(RelationMaxRouting)
	if err != nil {
		resp.failed(ctx, fmt.Errorf("create v1beta3 relation worker pool: %w", err))
		return
	}
	defer p.Release()

	for idx, qry := range request.QueryList {
		idx := idx
		qry := qry
		sendWg.Add(1)
		if submitErr := p.Submit(func() {
			defer sendWg.Done()
			queryCtx, querySpan := trace.NewSpan(ctx, "handler-api-relation-multi-resource-v1beta3-item")
			var queryErr error
			defer querySpan.End(&queryErr)
			querySpan.Set("query-index", idx)
			querySpan.Set("requested-source-type", string(qry.SourceType))
			querySpan.Set("requested-target-type", string(qry.TargetType))
			d := cmdb.RelationMultiResourceResponseData{
				Code: http.StatusOK,
			}

			timestamp := cast.ToString(qry.Timestamp)
			// v1beta3 默认 HTTP 协议对齐旧 VM relation，底层由 TimeGraph 提供关系查询。
			d.SourceType, d.SourceInfo, d.Path, d.TargetType, d.TargetList, queryErr = model.QueryResourceMatcher(
				queryCtx,
				qry.LookBackDelta, user.SpaceUID, timestamp,
				qry.TargetType, qry.SourceType,
				qry.SourceInfo, qry.SourceExpandInfo, qry.TargetInfoShow,
				qry.PathResource,
			)
			if queryErr != nil {
				d.Message = queryErr.Error()
				d.Code = http.StatusBadRequest
				d.Truncated, d.TruncatedReason = relationTruncationMetadata(queryErr)
			}

			if d.TargetList == nil {
				d.TargetList = make(cmdb.Matchers, 0)
			}
			querySpan.Set("response-code", d.Code)
			querySpan.Set("response-path-length", len(d.Path))
			querySpan.Set("response-target-count", len(d.TargetList))

			lock.Lock()
			data.Data[idx] = d
			if queryErr != nil {
				failedQueryCount++
			}
			lock.Unlock()
		}); submitErr != nil {
			sendWg.Done()
			_, querySpan := trace.NewSpan(ctx, "handler-api-relation-multi-resource-v1beta3-item")
			querySpan.Set("query-index", idx)
			querySpan.Set("failure-stage", "worker-submit")
			querySpan.End(&submitErr)
			lock.Lock()
			data.Data[idx] = cmdb.RelationMultiResourceResponseData{
				Code:       http.StatusBadRequest,
				Message:    submitErr.Error(),
				TargetList: make(cmdb.Matchers, 0),
			}
			failedQueryCount++
			lock.Unlock()
		}
	}
	sendWg.Wait()
	span.Set("query-count", len(request.QueryList))
	span.Set("failed-query-count", failedQueryCount)
	span.Set("successful-query-count", len(request.QueryList)-failedQueryCount)
	span.Set("partial-failure", failedQueryCount > 0)

	resp.success(ctx, data)
}

// HandlerAPIRelationV1Beta3MultiResourceRange
// @Summary  query relation multi resource range (v1beta3, TimeGraph)
// @ID       relation_multi_resource_query_range_v1beta3
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID"
// @Param    X-Bk-Scope-Space-Uid   header    string                          false  "空间UID" default(bkcc__2)
// @Param    data                  	body      cmdb.RelationMultiResourceRangeRequest			  true   "json data"
// @Success  200                   	{object}  cmdb.RelationMultiResourceRangeResponse
// @Failure  400                   	{object}  ErrResponse
// @Router   /api/v1/relation/v1beta3/multi_resource_range [post]
func HandlerAPIRelationV1Beta3MultiResourceRange(c *gin.Context) {
	var (
		ctx = c.Request.Context()

		user = metadata.GetUser(ctx)
		err  error

		resp = &response{
			c: c,
		}
	)

	ctx, span := trace.NewSpan(ctx, "handler-api-relation-multi-resource-range-v1beta3")
	defer span.End(&err)

	request := new(cmdb.RelationMultiResourceRangeRequest)
	err = json.NewDecoder(c.Request.Body).Decode(request)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	paramsBody, _ := json.Marshal(request)
	span.Set("handler-headers", c.Request.Header)
	span.Set("handler-body", string(paramsBody))
	span.Set("query-mode", metric.CMDBRelationQueryModeRange)
	metric.CMDBRelationQueryListSizeObserve(ctx, metric.CMDBRelationRouteTimeGraph, metric.CMDBRelationQueryModeRange, len(request.QueryList))

	model, err := v1beta3.GetModel(ctx)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	data := new(cmdb.RelationMultiResourceRangeResponse)
	data.TraceID = span.TraceID()
	data.Data = make([]cmdb.RelationMultiResourceRangeResponseData, len(request.QueryList))

	var (
		sendWg           sync.WaitGroup
		lock             sync.Mutex
		failedQueryCount int
	)
	p, err := ants.NewPool(RelationMaxRouting)
	if err != nil {
		resp.failed(ctx, fmt.Errorf("create v1beta3 relation range worker pool: %w", err))
		return
	}
	defer p.Release()

	for idx, qry := range request.QueryList {
		idx := idx
		qry := qry
		sendWg.Add(1)
		if submitErr := p.Submit(func() {
			defer sendWg.Done()
			queryCtx, querySpan := trace.NewSpan(ctx, "handler-api-relation-multi-resource-range-v1beta3-item")
			var queryErr error
			defer querySpan.End(&queryErr)
			querySpan.Set("query-index", idx)
			querySpan.Set("requested-source-type", string(qry.SourceType))
			querySpan.Set("requested-target-type", string(qry.TargetType))
			d := cmdb.RelationMultiResourceRangeResponseData{
				Code: http.StatusOK,
			}

			startTs := cast.ToString(qry.StartTs)
			endTs := cast.ToString(qry.EndTs)
			// range 保持 VM 兼容响应，target_list 的窗口语义在 v1beta3 model 内部对齐。
			d.SourceType, d.SourceInfo, d.Path, d.TargetType, d.TargetList, queryErr = model.QueryResourceMatcherRange(
				queryCtx,
				qry.LookBackDelta, user.SpaceUID, qry.Step, startTs, endTs,
				qry.TargetType, qry.SourceType,
				qry.SourceInfo, qry.SourceExpandInfo, qry.TargetInfoShow,
				qry.PathResource,
			)
			if queryErr != nil {
				d.Message = queryErr.Error()
				d.Code = http.StatusBadRequest
				d.Truncated, d.TruncatedReason = relationTruncationMetadata(queryErr)
			}

			if d.TargetList == nil {
				d.TargetList = make([]cmdb.MatchersWithTimestamp, 0)
			}
			if len(d.Path) > 0 {
				d.SourceType = cmdb.Resource(d.Path[0])
				d.TargetType = cmdb.Resource(d.Path[len(d.Path)-1])
			}
			querySpan.Set("response-code", d.Code)
			querySpan.Set("response-path-length", len(d.Path))
			querySpan.Set("response-bucket-count", len(d.TargetList))

			lock.Lock()
			data.Data[idx] = d
			if queryErr != nil {
				failedQueryCount++
			}
			lock.Unlock()
		}); submitErr != nil {
			sendWg.Done()
			_, querySpan := trace.NewSpan(ctx, "handler-api-relation-multi-resource-range-v1beta3-item")
			querySpan.Set("query-index", idx)
			querySpan.Set("failure-stage", "worker-submit")
			querySpan.End(&submitErr)
			lock.Lock()
			data.Data[idx] = cmdb.RelationMultiResourceRangeResponseData{
				Code:       http.StatusBadRequest,
				Message:    submitErr.Error(),
				TargetList: make([]cmdb.MatchersWithTimestamp, 0),
			}
			failedQueryCount++
			lock.Unlock()
		}
	}
	sendWg.Wait()
	span.Set("query-count", len(request.QueryList))
	span.Set("failed-query-count", failedQueryCount)
	span.Set("successful-query-count", len(request.QueryList)-failedQueryCount)
	span.Set("partial-failure", failedQueryCount > 0)

	resp.success(ctx, data)
}
