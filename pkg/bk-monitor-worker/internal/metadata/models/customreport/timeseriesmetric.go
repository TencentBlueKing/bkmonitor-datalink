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
	GroupID        uint      `json:"group_id" gorm:"unique"`
	TableID        string    `json:"table_id" gorm:"size:255"`
	FieldID        uint      `json:"field_id" gorm:"primary_key"`
	FieldName      string    `json:"field_name" gorm:"size:255;unique"`
	TagList        string    `json:"tag_list" sql:"type:text"`
	LastModifyTime time.Time `json:"last_modify_time" gorm:"column:last_modify_time"`
	LastIndex      uint      `json:"last_index"`
	Label          string    `json:"label" gorm:"size:255"`
	IsActive       bool      `json:"is_active" gorm:"column:is_active;default:true"`
}

// BeforeCreate 新建前时间字段设置为当前时间
func (s *TimeSeriesMetric) BeforeCreate(tx *gorm.DB) error {
	s.LastModifyTime = time.Now()
	return nil
}

// TableName table alias name
func (TimeSeriesMetric) TableName() string {
	return "metadata_timeseriesmetric"
}
