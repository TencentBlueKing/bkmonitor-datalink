// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package customreport

import (
	"time"

	"github.com/jinzhu/gorm"
)

//go:generate goqueryset -in timeseriesscope.go -out qs_tsscope_gen.go

// TimeSeriesScope: time series scope model
// gen:qs
type TimeSeriesScope struct {
	ID              uint      `gorm:"primary_key;AUTO_INCREMENT;column:id" json:"id"`
	GroupID         uint      `gorm:"column:group_id;type:int;index;unique_index:idx_group_scope" json:"group_id"`
	ScopeName       string    `gorm:"type:varchar(255);not null;column:scope_name;unique_index:idx_group_scope" json:"scope_name"`
	DimensionConfig string    `gorm:"type:json;column:dimension_config;default:'{}'" json:"dimension_config"`
	AutoRules       string    `gorm:"type:json;column:auto_rules;default:'[]'" json:"auto_rules"`
	CreateFrom      string    `gorm:"type:varchar(10);default:'data';column:create_from" json:"create_from"`
	LastModifyTime  time.Time `gorm:"type:datetime;column:last_modify_time" json:"last_modify_time"`
}

// BeforeCreate 新建前时间字段设置为当前时间
func (t *TimeSeriesScope) BeforeCreate(tx *gorm.DB) error {
	if t.DimensionConfig == "" {
		t.DimensionConfig = "{}"
	}
	if t.AutoRules == "" {
		t.AutoRules = "[]"
	}
	t.LastModifyTime = time.Now()
	return nil
}

// BeforeUpdate 更新前的钩子函数
func (t *TimeSeriesScope) BeforeUpdate(tx *gorm.DB) error {
	t.LastModifyTime = time.Now()
	return nil
}

// TableName table alias name
func (TimeSeriesScope) TableName() string {
	return "metadata_timeseriesscope"
}
