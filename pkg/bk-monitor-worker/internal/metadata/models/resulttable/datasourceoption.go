// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package resulttable

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
)

//go:generate goqueryset -in datasourceoption.go -out qs_datasourceoption_gen.go

// DataSourceOption data source option model
// gen:qs
type DataSourceOption struct {
	models.OptionBase
	BkTenantId string `gorm:"column:bk_tenant_id;size:256" json:"bk_tenant_id"`
	BkDataId   uint   `gorm:"column:bk_data_id" json:"bk_data_id"`
	Name       string `json:"name" gorm:"size:128"`
}

// TableName table alias name
func (DataSourceOption) TableName() string {
	return "metadata_datasourceoption"
}
