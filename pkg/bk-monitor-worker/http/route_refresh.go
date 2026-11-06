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
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	metadataTask "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/task"
)

func registerRouteRefresh(router *gin.RouterGroup) {
	routes := router.Group("/metadata/routes")
	routes.POST("/result-table/refresh", refreshResultTable)
	routes.POST("/data-label/refresh", refreshDataLabel)
	routes.POST("/space/refresh", refreshSpace)
}

func refreshResultTable(c *gin.Context) {
	var req struct {
		TenantID  string   `json:"bk_tenant_id" binding:"required"`
		TableIDs  []string `json:"table_ids" binding:"required,min=1,dive,required"`
		IsPublish *bool    `json:"is_publish"`
	}
	if err := BindJSON(c, &req); err != nil {
		BadReqResponse(c, "%s", err)
		return
	}
	if strings.TrimSpace(req.TenantID) == "" {
		BadReqResponse(c, "bk_tenant_id is required")
		return
	}
	publish := req.IsPublish == nil || *req.IsPublish
	err := service.NewSpacePusher().PushTableIdDetail(req.TenantID, req.TableIDs, publish)
	finishRouteRefresh(c, err)
}

func refreshDataLabel(c *gin.Context) {
	var req struct {
		TenantID   string   `json:"bk_tenant_id" binding:"required"`
		TableIDs   []string `json:"table_ids" binding:"omitempty,dive,required"`
		DataLabels []string `json:"data_labels" binding:"omitempty,dive,required"`
	}
	if err := BindJSON(c, &req); err != nil {
		BadReqResponse(c, "%s", err)
		return
	}
	if strings.TrimSpace(req.TenantID) == "" || (len(req.TableIDs) > 0) == (len(req.DataLabels) > 0) {
		BadReqResponse(c, "provide bk_tenant_id and exactly one non-empty list: table_ids or data_labels")
		return
	}
	p := service.NewSpacePusher()
	if len(req.DataLabels) > 0 {
		finishRouteRefresh(c, p.PushNamedDataLabels(req.TenantID, req.DataLabels))
		return
	}
	finishRouteRefresh(c, p.PushDataLabelTableIds(req.TenantID, req.TableIDs, true))
}

func refreshSpace(c *gin.Context) {
	var req struct {
		TenantID  string `json:"bk_tenant_id" binding:"required"`
		SpaceType string `json:"space_type" binding:"required,oneof=bkcc bkci bksaas"`
		SpaceID   string `json:"space_id" binding:"required"`
	}
	if err := BindJSON(c, &req); err != nil {
		BadReqResponse(c, "%s", err)
		return
	}
	if strings.TrimSpace(req.TenantID) == "" {
		BadReqResponse(c, "bk_tenant_id is required")
		return
	}
	err := metadataTask.RefreshSpaceRoute(req.TenantID, req.SpaceType, req.SpaceID)
	if errors.Is(err, metadataTask.ErrSpaceNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"result": false, "code": common.ParamsError, "message": "space not found", "data": nil})
		return
	}
	finishRouteRefresh(c, err)
}

func finishRouteRefresh(c *gin.Context, err error) {
	if err != nil {
		ServerErrResponse(c, "refresh route: %s", err)
		return
	}
	Response(c, nil)
}
