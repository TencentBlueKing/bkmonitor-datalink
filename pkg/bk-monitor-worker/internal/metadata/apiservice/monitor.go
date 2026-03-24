// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package apiservice

import (
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/monitor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
)

var Monitor MonitorService

type MonitorService struct{}

// SearchAlert 获取告警数据
func (MonitorService) SearchAlert(conditions []map[string]any, startTime int64, endTime int64, page int, pageSize int, bkBizID int32) (*monitor.SearchAlertData, error) {
	tenantId, err := tenant.GetTenantIdByBkBizId(int(bkBizID))
	if err != nil {
		return nil, errors.Wrapf(err, "GetTenantIdByBkBizId failed, bkBizID: %d", bkBizID)
	}

	monitorApi, err := api.GetMonitorApi(tenantId)
	if err != nil {
		return nil, errors.Wrap(err, "GetMonitorApi failed")
	}
	var resp monitor.SearchAlertResp
	params := map[string]any{
		"bk_biz_ids": []int{int(bkBizID)},
		"start_time": startTime,
		"end_time":   endTime,
		"page":       page,
		"page_size":  pageSize,
		"conditions": conditions,
	}
	_, err = monitorApi.SearchAlert().SetBody(params).SetResult(&resp).Request()
	if err != nil {
		return nil, errors.Wrap(err, "SearchAlert failed")
	}
	if err := resp.Err(); err != nil {
		return nil, errors.Wrap(err, "SearchAlert failed")
	}
	return &resp.Data, nil
}
