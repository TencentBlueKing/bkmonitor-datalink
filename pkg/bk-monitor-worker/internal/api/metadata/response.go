// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
)

type CustomTimeSeriesDetailResp struct {
	define.ApiCommonRespMeta
	Data CustomTimeSeriesDetailData `json:"data"`
}

type CustomTimeSeriesDetailData struct {
	TimeSeriesGroupId int                        `json:"time_series_group_id"`
	IsReadonly        bool                       `json:"is_readonly"`
	CreateTime        string                     `json:"create_time"`
	CreateUser        string                     `json:"create_user"`
	UpdateTime        string                     `json:"update_time"`
	UpdateUser        string                     `json:"update_user"`
	IsDeleted         bool                       `json:"is_deleted"`
	BkDataId          int                        `json:"bk_data_id"`
	BkBizId           int                        `json:"bk_biz_id"`
	Name              string                     `json:"name"`
	Scenario          string                     `json:"scenario"`
	TableId           string                     `json:"table_id"`
	IsPlatform        bool                       `json:"is_platform"`
	DataLabel         string                     `json:"data_label"`
	Protocol          string                     `json:"protocol"`
	Desc              string                     `json:"desc"`
	ScenarioDisplay   []string                   `json:"scenario_display"`
	AccessToken       string                     `json:"access_token"`
	MetricJson        []map[string][]interface{} `json:"metric_json"`
	Target            []interface{}              `json:"target"`
}
