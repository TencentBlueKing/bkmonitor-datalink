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

//go:generate goqueryset -in datasourceresulttable.go -out qs_datasourceresulttable_gen.go

// DataSourceResultTable data source result table model
// gen:qs
type DataSourceResultTable struct {
	BkTenantId string    `gorm:"column:bk_tenant_id;size:256" json:"bk_tenant_id"`
	BkDataId   uint      `gorm:"column:bk_data_id;" json:"bk_data_id"`
	TableId    string    `gorm:"size:128" json:"table_id"`
	Creator    string    `gorm:"size:32" json:"creator"`
	CreateTime time.Time `gorm:"create_time;" json:"create_time"`
}

// BeforeCreate 新建前时间字段设置为当前时间
func (d *DataSourceResultTable) BeforeCreate(tx *gorm.DB) error {
	d.CreateTime = time.Now()
	return nil
}

// TableName table alias name
func (DataSourceResultTable) TableName() string {
	return "metadata_datasourceresulttable"
}
