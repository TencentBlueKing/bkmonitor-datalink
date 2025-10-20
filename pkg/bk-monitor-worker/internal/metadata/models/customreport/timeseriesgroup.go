// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package customreport

//go:generate goqueryset -in timeseriesgroup.go -out qs_tsgroup_gen.go

// TimeSeriesGroup : time series group model
// gen:qs
type TimeSeriesGroup struct {
	CustomGroupBase
	BkTenantId          string `gorm:"column:bk_tenant_id;size:256" json:"bk_tenant_id"`
	TimeSeriesGroupID   uint   `json:"time_series_group_id" gorm:"unique;primary_key"`
	TimeSeriesGroupName string `json:"time_series_group_name" gorm:"size:255"`
}

// TableName : 用于设置表的别名
func (TimeSeriesGroup) TableName() string {
	return "metadata_timeseriesgroup"
}
