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

//go:generate goqueryset -in resulttable.go -out qs_resulttable_gen.go

// ResultTable result table model
// gen:qs
type ResultTable struct {
	TableId        string    `gorm:"table_id;primary_key" json:"table_id"`
	TableNameZh    string    `gorm:"table_name_zh;size:128" json:"table_name_zh"`
	IsCustomTable  bool      `gorm:"is_custom_table" json:"is_custom_table"`
	SchemaType     string    `gorm:"schema_type;size:64" json:"schema_type"`
	DefaultStorage string    `gorm:"default_storage" json:"default_storage"`
	Creator        string    `gorm:"creator;size:32" json:"creator"`
	CreateTime     time.Time `gorm:"create_time;" json:"create_time"`
	LastModifyUser string    `gorm:"last_modify_user;size:32" json:"last_modify_user"`
	LastModifyTime time.Time `gorm:"last_modify_time" json:"last_modify_time"`
	BkBizId        int       `gorm:"bk_biz_id" json:"bk_biz_id"`
	IsDeleted      bool      `gorm:"is_deleted" json:"is_deleted"`
	Label          string    `gorm:"label;size:128" json:"label"`
	IsEnable       bool      `gorm:"is_enable" json:"is_enable"`
	DataLabel      *string   `gorm:"data_label;size:128" json:"data_label"`
	IsBuiltin      bool      `gorm:"column:is_builtin" json:"is_builtin"`
	BkBizIdAlias   string    `gorm:"bk_biz_id_alias;size:128" json:"bk_biz_id_alias"`
	BkTenantId     string    `gorm:"bk_tenant_id;size:256" json:"bk_tenant_id"`
}

// BeforeCreate 新建前时间字段设置为当前时间
func (e *ResultTable) BeforeCreate(tx *gorm.DB) error {
	e.CreateTime = time.Now()
	e.LastModifyTime = time.Now()
	return nil
}

// TableName table alias name
func (ResultTable) TableName() string {
	return "metadata_resulttable"
}
