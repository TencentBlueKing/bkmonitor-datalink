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
	"context"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errno"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/endpoint"
)

func RegisterRelation(ctx context.Context, g *gin.RouterGroup) {
	registerHandler := endpoint.NewRegisterHandler(ctx, g)

	registerHandler.Register("POST", RelationMultiResource, HandlerAPIRelationMultiResource)
	registerHandler.Register("POST", RelationMultiResourceRange, HandlerAPIRelationMultiResourceRange)
	codedInfo1 := errno.ErrInfoAPICall().
		WithComponent("API注册").
		WithOperation("注册Relation接口").
		WithContext("路由", "[POST] "+RelationMultiResource)
	log.InfoWithCodef(ctx, codedInfo1)

	codedInfo2 := errno.ErrInfoAPICall().
		WithComponent("API注册").
		WithOperation("注册Relation范围接口").
		WithContext("路由", "[POST] "+RelationMultiResourceRange)
	log.InfoWithCodef(ctx, codedInfo2)
}
