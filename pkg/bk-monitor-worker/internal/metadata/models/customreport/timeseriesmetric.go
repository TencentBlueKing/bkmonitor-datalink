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

//go:generate goqueryset -in timeseriesmetric.go -out qs_tsmetric_gen.go

// TimeSeriesMetric: time series metric model
// gen:qs
type TimeSeriesMetric struct {
	FieldID        uint      `gorm:"primary_key;AUTO_INCREMENT;column:field_id" json:"field_id"`
	GroupID        uint      `gorm:"type:int;column:group_id;index;unique_index:idx_group_scope_name" json:"group_id"`
	ScopeID        uint      `gorm:"type:int;column:scope_id;index" json:"scope_id"`
	TableID        string    `gorm:"type:varchar(255);column:table_id" json:"table_id"`
	FieldScope     string    `gorm:"type:varchar(255);default:'default';column:field_scope;unique_index:idx_group_scope_name" json:"field_scope"`
	FieldName      string    `gorm:"type:varchar(255);not null;column:field_name;unique_index:idx_group_scope_name" json:"field_name"`
	TagList        string    `gorm:"type:json;column:tag_list;default:'[]'" json:"tag_list"`
	FieldConfig    string    `gorm:"type:json;column:field_config;default:'{}'" json:"field_config"`
	CreateTime     time.Time `gorm:"type:datetime;column:create_time" json:"create_time"`
	LastModifyTime time.Time `gorm:"type:datetime;column:last_modify_time" json:"last_modify_time"`
	Label          string    `gorm:"type:varchar(255);column:label" json:"label"`
	IsActive       bool      `gorm:"type:bool;default:true;column:is_active" json:"is_active"`
	LastIndex      uint      `gorm:"type:int;column:last_index" json:"last_index"`
}

// BeforeCreate 新建前时间字段设置为当前时间
func (s *TimeSeriesMetric) BeforeCreate(tx *gorm.DB) error {
	if s.CreateTime.IsZero() {
		s.CreateTime = time.Now()
	}
	if s.LastModifyTime.IsZero() {
		s.LastModifyTime = time.Now()
	}
	if s.TagList == "" {
		s.TagList = "[]"
	}
	if s.FieldConfig == "" {
		s.FieldConfig = "{}"
	}

	return nil
}

// TableName table alias name
func (TimeSeriesMetric) TableName() string {
	return "metadata_timeseriesmetric"
}
