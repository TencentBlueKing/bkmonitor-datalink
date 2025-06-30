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
	"time"

	"github.com/jinzhu/gorm"
)

//go:generate goqueryset -in datasource.go -out qs_datasource_gen.go

// DataSource datasource model
// gen:qs
type DataSource struct {
	BkTenantId             string    `gorm:"column:bk_tenant_id;size:256" json:"bk_tenant_id"`
	BkDataId               uint      `gorm:"primary_key" json:"bk_data_id"`
	Token                  string    `gorm:"size:32" json:"token"`
	DataName               string    `gorm:"size:128;index" json:"data_name"`
	DataDescription        string    `gorm:"type:text;" json:"data_description"`
	MqClusterId            uint      `gorm:"column:mq_cluster_id" json:"mq_cluster_id"`
	MqConfigId             uint      `gorm:"column:mq_config_id" json:"mq_config_id"`
	EtlConfig              string    `gorm:"type:text;" json:"etl_config"`
	IsCustomSource         bool      `gorm:"is_custom_source" json:"is_custom_source"`
	Creator                string    `gorm:"size:32" json:"creator"`
	CreateTime             time.Time `gorm:"create_time;" json:"create_time"`
	LastModifyUser         string    `gorm:"last_modify_user;size:32" json:"last_modify_user"`
	LastModifyTime         time.Time `gorm:"last_modify_time" json:"last_modify_time"`
	TypeLabel              string    `gorm:"size:128" json:"type_label"`
	SourceLabel            string    `gorm:"size:128" json:"source_label"`
	CustomLabel            *string   `gorm:"size:256" json:"custom_label"`
	SourceSystem           string    `gorm:"size:256" json:"source_system"`
	IsEnable               bool      `gorm:"is_enable" json:"is_enable"`
	TransferClusterId      string    `gorm:"size:50" json:"transfer_cluster_id"`
	IsPlatformDataId       bool      `gorm:"column:is_platform_data_id" json:"is_platform_data_id"`
	SpaceTypeId            string    `gorm:"size:64" json:"space_type_id"`
	SpaceUid               string    `gorm:"size:256" json:"space_uid"`
	CreatedFrom            string    `gorm:"size:16" json:"created_from"`
	IsTenantSpecificGlobal bool      `gorm:"column:is_tenant_specific_global" json:"is_tenant_specific_global"`
}

// BeforeCreate 新建前时间字段设置为当前时间
func (d *DataSource) BeforeCreate(tx *gorm.DB) error {
	d.CreateTime = time.Now()
	d.LastModifyTime = time.Now()
	if d.SpaceTypeId == "" {
		d.SpaceTypeId = "all"
	}
	return nil
}

// BeforeUpdate 保存前最后修改时间字段设置为当前时间
func (d *DataSource) BeforeUpdate(tx *gorm.DB) error {
	d.LastModifyTime = time.Now()
	return nil
}

// TableName table alias name
func (DataSource) TableName() string {
	return "metadata_datasource"
}
