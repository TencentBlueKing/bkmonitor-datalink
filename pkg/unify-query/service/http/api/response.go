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
	"fmt"
	"net/http"
	"unsafe"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errno"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
)

// ErrResponse 输出结构体
type ErrResponse struct {
	Err string `json:"error"`
}

type response struct {
	c *gin.Context
}

func (r *response) failed(ctx context.Context, err error) {
	codedErr := errno.ErrBusinessLogicError().
		WithComponent("HTTP API响应").
		WithOperation("处理API请求失败").
		WithContext("url", r.c.Request.URL.Path).
		WithContext("method", r.c.Request.Method).
		WithContext("error", err.Error()).
		WithSolution("检查API请求参数和服务状态")
	log.ErrorWithCodef(ctx, codedErr)
	user := metadata.GetUser(ctx)
	metric.APIRequestInc(ctx, r.c.Request.URL.Path, metric.StatusFailed, user.SpaceUID, user.Source)
	// 需要阻止响应返回, 交给统一响应处理
	if _, ok := r.c.Get(proxy.ContextConfigUnifyResponseProcess); ok {
		r.c.Set(proxy.ContextKeyResponseError, err)
		return
	}

	r.c.JSON(http.StatusBadRequest, ErrResponse{
		Err: err.Error(),
	})
}

func (r *response) success(ctx context.Context, data any) {
	log.Debugf(ctx, "query data size is %s", fmt.Sprint(unsafe.Sizeof(data)))
	user := metadata.GetUser(ctx)
	metric.APIRequestInc(ctx, r.c.Request.URL.Path, metric.StatusSuccess, user.SpaceUID, user.Source)
	// 同上
	if r.isConfigUnifyRespProcess(r.c) {
		r.c.Set(proxy.ContextKeyResponseData, data)
		return
	}
	r.c.JSON(http.StatusOK, data)
}

func (r *response) isConfigUnifyRespProcess(c *gin.Context) bool {
	_, isUnifyRespProcess := c.Get(proxy.ContextConfigUnifyResponseProcess)
	return isUnifyRespProcess
}
