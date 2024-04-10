// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"strings"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// DataFlowSvc dataflow service
type DataFlowSvc struct{}

func NewDataFlowSvc() DataFlowSvc {
	return DataFlowSvc{}
}
func (s DataFlowSvc) checkHasPermission(projectId int, rtId string) bool {
	hasPermission, err := apiservice.Bkdata.AuthProjectsDataCheck(projectId, rtId, "")
	if err != nil {
		logger.Errorf("check whether the project [%d] has the permission of [%s] table failed, %v.", projectId, rtId, err)
		return false
	}
	return hasPermission
}

func (s DataFlowSvc) EnsureHasPermissionWithRtId(rtId string, projectId int) bool {
	if projectId == 0 {
		projectId = cfg.BkdataProjectId
	}
	if !s.checkHasPermission(projectId, rtId) {
		// 针对结果表直接授权给项目
		result, err := apiservice.Bkdata.AuthResultTable(projectId, rtId, strings.Split(rtId, "_")[0])
		if err != nil {
			logger.Errorf("failed to grant permission [%s], %v", rtId, err)
			return false
		}
		logger.Infof("grant permission successfully [%s], result [%v]", rtId, result)
		return true
	}
	return true
}
