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
	"fmt"
	"net/http"
	"unsafe"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type response struct {
	c *gin.Context
}

func (r *response) failed(ctx context.Context, err error) {
	log.Errorf(ctx, err.Error())
	user := metadata.GetUser(ctx)
	metric.APIRequestInc(ctx, r.c.Request.URL.Path, metric.StatusFailed, user.SpaceUID, user.Source)

	_, span := trace.NewSpan(ctx, "response-failed")
	r.c.JSON(http.StatusBadRequest, ErrResponse{
		TraceID: span.TraceID(),
		Err:     err.Error(),
	})
}

func (r *response) success(ctx context.Context, data interface{}) {
	log.Debugf(ctx, "query data size is %s", fmt.Sprint(unsafe.Sizeof(data)))
	user := metadata.GetUser(ctx)
	metric.APIRequestInc(ctx, r.c.Request.URL.Path, metric.StatusSuccess, user.SpaceUID, user.Source)
	r.c.JSON(http.StatusOK, data)
}

// ListData 数据返回格式
type ListData struct {
	Total              int64                       `json:"total,omitempty"`
	List               []map[string]any            `json:"list" json:"list,omitempty"`
	TraceID            string                      `json:"trace_id,omitempty"`
	Status             *metadata.Status            `json:"status,omitempty" json:"status,omitempty"`
	ResultTableOptions metadata.ResultTableOptions `json:"result_table_options,omitempty"`
}
