// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package space

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"

// VMShortLinkRecord vm short link access record model.
type VMShortLinkRecord struct {
	ID                uint   `gorm:"column:id;primaryKey" json:"id"`
	BkTenantId        string `gorm:"column:bk_tenant_id;size:256" json:"bk_tenant_id"`
	SpaceType         string `gorm:"column:space_type;size:64" json:"space_type"`
	SpaceId           string `gorm:"column:space_id;size:128" json:"space_id"`
	TableId           string `gorm:"column:table_id;size:128" json:"table_id"`
	VMResultTableId   string `gorm:"column:vm_result_table_id;size:128" json:"vm_result_table_id"`
	VMResultTableName string `gorm:"column:vm_result_table_name;size:255" json:"vm_result_table_name"`
	VMClusterId       int    `gorm:"column:vm_cluster_id" json:"vm_cluster_id"`
	IsGlobal          bool   `gorm:"column:is_global" json:"is_global"`
	QueryRouterConfig string `gorm:"column:query_router_config;type:json" json:"query_router_config"`
	IsEnabled         bool   `gorm:"column:is_enabled" json:"is_enabled"`
	IsDeleted         bool   `gorm:"column:is_deleted" json:"is_deleted"`
	models.BaseModel
}

// TableName table alias name.
func (VMShortLinkRecord) TableName() string {
	return "metadata_vmshortlinkrecord"
}
