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

// CustomGroupBase : custom group base info for time series、event
type CustomGroupBase struct {
	BkDataID            uint      `json:"bk_data_id" gorm:"index"`
	BkBizID             int       `json:"bk_biz_id" gorm:"index"`
	TableID             string    `json:"table_id" gorm:"size:128;index"`
	MaxRate             int       `json:"max_rate" gorm:"column:max_rate"`
	Label               string    `json:"label" gorm:"size:128"`
	IsEnable            bool      `json:"is_enable" gorm:"column:is_enable"`
	IsDelete            bool      `json:"is_delete" gorm:"column:is_delete"`
	Creator             string    `json:"creator" gorm:"size:255"`
	CreateTime          time.Time `json:"create_time"`
	LastModifyUser      string    `json:"last_modify_user" gorm:"size:32"`
	LastModifyTime      time.Time `json:"last_modify_time"`
	IsSplitMeasurement  bool      `json:"is_split_measurement" gorm:"column:is_split_measurement"`
	MaxFutureTimeOffset int       `json:"max_future_time_offset" gorm:"column:max_future_time_offset"`
}

// BeforeCreate 默认值
func (c *CustomGroupBase) BeforeCreate(tx *gorm.DB) error {
	if c.MaxRate == 0 {
		c.MaxRate = -1
	}
	if c.Label == "" {
		c.Label = "other"
	}
	c.CreateTime = time.Now()
	c.LastModifyTime = time.Now()
	return nil
}
