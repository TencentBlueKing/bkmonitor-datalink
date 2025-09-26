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
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	ants "github.com/panjf2000/ants/v2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errno"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
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

	model, err := v1beta1.GetModel(ctx)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	data := new(cmdb.RelationMultiResourceResponse)
	data.TraceID = span.TraceID()
	data.Data = make([]cmdb.RelationMultiResourceResponseData, len(request.QueryList))

	var (
		sendWg sync.WaitGroup
		lock   sync.Mutex
	)
	p, _ := ants.NewPool(RelationMaxRouting)
	defer p.Release()

	for idx, qry := range request.QueryList {
		idx := idx
		qry := qry
		sendWg.Add(1)
		_ = p.Submit(func() {
			defer sendWg.Done()
			d := cmdb.RelationMultiResourceResponseData{
				Code: http.StatusOK,
			}

			d.SourceType, d.SourceInfo, d.Path, d.TargetType, d.TargetList, err = model.QueryResourceMatcher(ctx, qry.LookBackDelta, user.SpaceUID, qry.Timestamp, qry.TargetType, qry.SourceType, qry.SourceInfo, qry.SourceExpandInfo, qry.TargetInfoShow, qry.PathResource)
			if err != nil {
				d.Message = err.Error()
				d.Code = http.StatusBadRequest
			}

			// 返回给到 saas 的数据，不能为 null，必须要是 []，否则会报错
			if d.TargetList == nil {
				d.TargetList = make(cmdb.Matchers, 0)
			}

			lock.Lock()
			data.Data[idx] = d
			lock.Unlock()
		})
	}
	sendWg.Wait()

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

	model, err := v1beta1.GetModel(ctx)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	data := new(cmdb.RelationMultiResourceRangeResponse)
	data.TraceID = span.TraceID()
	data.Data = make([]cmdb.RelationMultiResourceRangeResponseData, len(request.QueryList))

	var (
		sendWg sync.WaitGroup
		lock   sync.Mutex
	)
	p, _ := ants.NewPool(RelationMaxRouting)
	defer p.Release()

	for idx, qry := range request.QueryList {
		idx := idx
		qry := qry
		sendWg.Add(1)
		_ = p.Submit(func() {
			defer sendWg.Done()
			d := cmdb.RelationMultiResourceRangeResponseData{
				Code: http.StatusOK,
			}

			d.SourceType, d.SourceInfo, d.Path, d.TargetType, d.TargetList, err = model.QueryResourceMatcherRange(ctx, qry.LookBackDelta, user.SpaceUID, qry.Step, qry.StartTs, qry.EndTs, qry.TargetType, qry.SourceType, qry.SourceInfo, qry.SourceExpandInfo, qry.TargetInfoShow, qry.PathResource)
			if err != nil {
				codedErr := errno.ErrBusinessQueryExecution().
					WithComponent("HTTP API").
					WithOperation("查询资源匹配范围").
					WithContext("space_uid", user.SpaceUID).
					WithContext("target_type", qry.TargetType).
					WithContext("source_type", qry.SourceType).
					WithContext("error", err.Error()).
					WithSolution("检查查询参数和资源配置")
				log.ErrorWithCodef(ctx, codedErr)

				d.Message = err.Error()
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

			lock.Lock()
			data.Data[idx] = d
			lock.Unlock()
		})
	}
	sendWg.Wait()

	resp.success(ctx, data)
}
