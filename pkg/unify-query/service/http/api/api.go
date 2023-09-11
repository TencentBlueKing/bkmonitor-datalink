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
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// HandlerAPIRelationMultiResource
// @Summary  query relation multi resource
// @ID       api-relation-multi-resource
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    X-Bk-Scope-Space-Uid   header    string                          false  "空间UID" default(bkcc__2)
// @Param    data                  	body      v1beta1.RelationMultiResourceRequest			  true   "json data"
// @Success  200                   	{object}  PromData
// @Failure  400                   	{object}  ErrResponse
// @Router   /api/v1/relation/multi_resource [post]
func HandlerAPIRelationMultiResource(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		span oleltrace.Span
		user = metadata.GetUser(ctx)
		err  error

		resp = &response{
			c: c,
		}
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "api-relation-multi-resource")
	if span != nil {
		defer span.End()
	}

	request := new(cmdb.RelationMultiResourceRequest)
	err = json.NewDecoder(c.Request.Body).Decode(request)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	paramsBody, _ := json.Marshal(request)
	trace.InsertStringIntoSpan("params-body", string(paramsBody), span)

	model, err := v1beta1.GetModel(ctx)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	data := new(cmdb.RelationMultiResourceResponse)
	data.Data = make([]cmdb.RelationMultiResourceResponseData, 0, len(request.QueryList))
	for _, qry := range request.QueryList {
		d := cmdb.RelationMultiResourceResponseData{
			Code: http.StatusOK,
		}

		d.SourceType, d.SourceInfo, d.TargetList, err = model.GetResourceMatcher(ctx, user.SpaceUid, qry.Timestamp, qry.TargetType, qry.SourceInfo)
		if err != nil {
			d.Message = err.Error()
			d.Code = http.StatusBadRequest
		}
		data.Data = append(data.Data, d)
	}

	resp.success(ctx, data)
}
