// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type response struct {
	c *gin.Context
}

func (r *response) failed(ctx context.Context, err error) {
	user := metadata.GetUser(ctx)
	metric.APIRequestInc(ctx, r.c.Request.URL.Path, metric.StatusFailed, user.SpaceUID, user.Source)

	if _, ok := r.c.Get(proxy.ContextConfigUnifyResponseProcess); ok {
		r.c.Set(proxy.ContextKeyResponseError, err)
		return
	}

	r.c.JSON(http.StatusBadRequest, ErrResponse{
		TraceID: trace.TraceIDFromContext(ctx),
		Err:     err.Error(),
	})
}

func (r *response) success(ctx context.Context, data any) {
	user := metadata.GetUser(ctx)
	metric.APIRequestInc(ctx, r.c.Request.URL.Path, metric.StatusSuccess, user.SpaceUID, user.Source)
	isUnifyRespProcess := r.isConfigUnifyRespProcess(r.c)
	if isUnifyRespProcess {
		r.c.Set(proxy.ContextKeyResponseData, data)
		return
	}
	r.c.JSON(http.StatusOK, data)
}

func (r *response) isConfigUnifyRespProcess(c *gin.Context) bool {
	_, isUnifyRespProcess := c.Get(proxy.ContextConfigUnifyResponseProcess)
	return isUnifyRespProcess
}

// ListData 数据返回格式
type ListData struct {
	Total              int64                       `json:"total"`
	List               []map[string]any            `json:"list"`
	Done               bool                        `json:"done"`
	TraceID            string                      `json:"trace_id,omitempty"`
	Status             *metadata.Status            `json:"status"`
	ResultTableOptions metadata.ResultTableOptions `json:"result_table_options,omitempty"`
	// ResultTableID 来自 QueryReference 路由解析结果，不随分页、scroll 或返回行裁剪。
	ResultTableID []string `json:"result_table_id"`
}

// resultTableIDFromRouteInfo 在响应出口把内部路由摘要投影成 RT 列表。
func resultTableIDFromRouteInfo(routeInfo []metadata.RouteInfo) []string {
	seen := make(map[string]struct{}, len(routeInfo))
	resultTableID := make([]string, 0, len(routeInfo))
	for _, info := range routeInfo {
		if info.TableID == "" {
			continue
		}
		if _, ok := seen[info.TableID]; ok {
			continue
		}
		seen[info.TableID] = struct{}{}
		resultTableID = append(resultTableID, info.TableID)
	}
	sort.Strings(resultTableID)
	return resultTableID
}

// DataResponse 返回数据结构体
type DataResponse struct {
	Data    any    `json:"data"`
	TraceID string `json:"trace_id,omitempty"`
}

type ErrResponse struct {
	TraceID string `json:"trace_id,omitempty"`
	Err     string `json:"error"`
}
