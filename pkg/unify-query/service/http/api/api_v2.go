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
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v2"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// HandlerAPIRelationMultiResourceV2
// @Summary  query relation multi resource v2 (using BKBase SurrealDB)
// @ID       relation_multi_resource_query_v2
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    X-Bk-Scope-Space-Uid   header    string                          false  "空间UID" default(bkcc__2)
// @Param    data                  	body      cmdb.RelationMultiResourceRequest			  true   "json data"
// @Success  200                   	{object}  cmdb.RelationMultiResourceResponseV2
// @Failure  400                   	{object}  ErrResponse
// @Router   /api/v2/relation/multi_resource [post]
func HandlerAPIRelationMultiResourceV2(c *gin.Context) {
	var (
		ctx = c.Request.Context()

		user = metadata.GetUser(ctx)
		err  error

		resp = &response{
			c: c,
		}
	)

	ctx, span := trace.NewSpan(ctx, "handler-api-relation-multi-resource-v2")
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

	model, err := v2.GetModel(ctx)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	data := new(cmdb.RelationMultiResourceResponseV2)
	data.TraceID = span.TraceID()
	data.Data = make([]cmdb.RelationMultiResourceResponseDataV2, len(request.QueryList))

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
			d := cmdb.RelationMultiResourceResponseDataV2{
				Code: http.StatusOK,
			}

			timestamp := cast.ToString(qry.Timestamp)
			d.SourceType, d.SourceInfo, d.Path, d.TargetType, d.TargetList, err = model.QueryResourceMatcher(ctx, qry.LookBackDelta, user.SpaceUID, timestamp, qry.TargetType, qry.SourceType, qry.SourceInfo, qry.SourceExpandInfo, qry.TargetInfoShow, qry.PathResource)
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

// HandlerAPIRelationMultiResourceRangeV2
// @Summary  query relation multi resource range v2 (using BKBase SurrealDB)
// @ID       relation_multi_resource_query_range_v2
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    X-Bk-Scope-Space-Uid   header    string                          false  "空间UID" default(bkcc__2)
// @Param    data                  	body      cmdb.RelationMultiResourceRangeRequest			  true   "json data"
// @Success  200                   	{object}  cmdb.RelationMultiResourceRangeResponseV2
// @Failure  400                   	{object}  ErrResponse
// @Router   /api/v2/relation/multi_resource_range [post]
func HandlerAPIRelationMultiResourceRangeV2(c *gin.Context) {
	var (
		ctx = c.Request.Context()

		user = metadata.GetUser(ctx)
		err  error

		resp = &response{
			c: c,
		}
	)

	ctx, span := trace.NewSpan(ctx, "handler-api-relation-multi-resource-range-v2")
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

	model, err := v2.GetModel(ctx)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	data := new(cmdb.RelationMultiResourceRangeResponseV2)
	data.TraceID = span.TraceID()
	data.Data = make([]cmdb.RelationMultiResourceRangeResponseDataV2, len(request.QueryList))

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
			d := cmdb.RelationMultiResourceRangeResponseDataV2{
				Code: http.StatusOK,
			}

			startTs := cast.ToString(qry.StartTs)
			endTs := cast.ToString(qry.EndTs)
			d.SourceType, d.SourceInfo, d.Path, d.TargetType, d.TargetList, err = model.QueryResourceMatcherRange(ctx, qry.LookBackDelta, user.SpaceUID, qry.Step, startTs, endTs, qry.TargetType, qry.SourceType, qry.SourceInfo, qry.SourceExpandInfo, qry.TargetInfoShow, qry.PathResource)
			if err != nil {
				d.Message = metadata.NewMessage(
					metadata.MsgQueryRelation,
					"关联数据查询异常",
				).Error(ctx, err).Error()
				d.Code = http.StatusBadRequest
			}

			if len(d.Path) > 0 {
				d.SourceType = cmdb.Resource(d.Path[0].Steps[0].ResourceType)
				d.TargetType = cmdb.Resource(d.Path[0].Steps[len(d.Path[0].Steps)-1].ResourceType)
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
