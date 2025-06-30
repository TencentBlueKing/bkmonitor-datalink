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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

//go:generate goqueryset -in resulttablefield.go -out qs_rtfield_gen.go

// ResultTableField: result table field model
// gen:qs
type ResultTableField struct {
	BkTenantId     string    `gorm:"column:bk_tenant_id;size:256" json:"bk_tenant_id"`
	Id             uint      `json:"id" gorm:"primary_key"`
	TableID        string    `json:"table_id" gorm:"size:128;unique"`
	FieldName      string    `json:"field_name" gorm:"size:255;unique"`
	FieldType      string    `json:"field_type" gorm:"size:32"`
	Description    string    `json:"description" gorm:"type:text"`
	Unit           string    `json:"unit" gorm:"size:32"`
	Tag            string    `json:"tag" gorm:"size:16"`
	IsConfigByUser bool      `json:"is_config_by_user"`
	DefaultValue   *string   `json:"default_value" gorm:"size:128"`
	Creator        string    `json:"creator" gorm:"size:32"`
	CreateTime     time.Time `json:"create_time" gorm:"column:create_time"`
	LastModifyUser string    `json:"last_modify_user" gorm:"size:32"`
	LastModifyTime time.Time `json:"last_modify_time" gorm:"column:last_modify_time"`
	AliasName      string    `json:"alias_name" gorm:"size:64"`
	IsDisabled     bool      `json:"is_disabled" gorm:"column:is_disabled"`
}

// TableName table alias name
func (ResultTableField) TableName() string {
	return "metadata_resulttablefield"
}

// BeforeCreate 新建前时间字段设置为当前时间
func (rtf *ResultTableField) BeforeCreate(tx *gorm.DB) error {
	rtf.LastModifyTime = time.Now().UTC()
	rtf.CreateTime = time.Now().UTC()
	return nil
}

// UpdateMetricFieldFromTS update result table metric field
func (rtf *ResultTableField) UpdateMetricFieldFromTS() error {
	if _, _, err := rtf.GetOrCreate(); err != nil {
		return err
	}
	logger.Infof("table: [%s] metric field: [%s] is updated", rtf.TableID, rtf.FieldName)
	return nil
}

// GetOrCreate retrieve or create a record, and return the record info
func (rtf *ResultTableField) GetOrCreate() (*ResultTableField, bool, error) {
	dbSession := mysql.GetDBSession()
	qs := NewResultTableFieldQuerySet(dbSession.DB)
	qs = qs.BkTenantIdEq(rtf.BkTenantId).TableIDEq(rtf.TableID).FieldNameEq(rtf.FieldName)

	var rtfRecord ResultTableField
	created := false
	if err := qs.One(&rtfRecord); err != nil {
		created = true
		// create a record
		if err := rtf.Create(dbSession.DB); err != nil {
			return nil, created, err
		}
		// 查询数据，然后返回
		if err := qs.One(&rtfRecord); err != nil {
			return nil, created, err
		}
	}

	return &rtfRecord, created, nil
}
