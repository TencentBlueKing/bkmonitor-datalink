// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package endpoint

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errno"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

var registerHandler *RegisterHandler

type RegisterHandler struct {
	ctx context.Context
	g   *gin.RouterGroup
}

func (r *RegisterHandler) Register(method, handlerPath string, handlerFunc ...gin.HandlerFunc) {
	// 记录注册的路由和处理函数,方便进行统一处理
	metadata.AddHandler(handlerPath, handlerFunc...)
	r.RegisterWithOutHandlerMap(method, handlerPath, handlerFunc...)
}

func (r *RegisterHandler) RegisterWithOutHandlerMap(method, handlerPath string, handlerFunc ...gin.HandlerFunc) {
	switch method {
	case http.MethodGet:
		r.g.GET(handlerPath, handlerFunc...)
	case http.MethodPost:
		r.g.POST(handlerPath, handlerFunc...)
	case http.MethodHead:
		r.g.HEAD(handlerPath, handlerFunc...)
	default:
		codedErr := errno.ErrBusinessLogicError().
			WithComponent("端点注册").
			WithOperation("注册处理器").
			WithContext("方法类型", method).
			WithSolution("检查HTTP方法类型是否支持")
		log.ErrorWithCodef(r.ctx, codedErr)
		return
	}
}

func NewRegisterHandler(ctx context.Context, g *gin.RouterGroup) *RegisterHandler {
	if registerHandler == nil {
		registerHandler = &RegisterHandler{
			ctx: ctx,
			g:   g,
		}
	}
	return registerHandler
}
