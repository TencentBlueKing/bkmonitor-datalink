// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package space

import (
	"fmt"

	"github.com/jinzhu/gorm"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
)

//go:generate goqueryset -in space.go -out qs_space_gen.go

// Space space model
// gen:qs
type Space struct {
	BkTenantId  string `gorm:"size:256" json:"bk_tenant_id"`
	Id          int    `gorm:"primary_key" json:"id"`
	SpaceTypeId string `gorm:"size:64" json:"spaceTypeId"`
	SpaceId     string `gorm:"size:128" json:"space_id"`
	SpaceName   string `gorm:"size:256" json:"space_name"`
	SpaceCode   string `gorm:"size:64" json:"space_code"`
	Status      string `gorm:"size:32" json:"status"`
	TimeZone    string `gorm:"size:32" json:"time_zone"`
	Language    string `gorm:"size:16" json:"language"`
	IsBcsValid  bool   `gorm:"column:is_bcs_valid" json:"is_bcs_valid"`
	models.BaseModel
}

// TableName table alias name
func (Space) TableName() string {
	return "metadata_space"
}

func (s *Space) BeforeCreate(tx *gorm.DB) error {
	_ = s.BaseModel.BeforeCreate(tx)
	if s.Status == "" {
		s.Status = "normal"
	}
	if s.TimeZone == "" {
		s.TimeZone = "Asia/Shanghai"
	}
	if s.Language == "" {
		s.Language = "zh-hans"
	}
	return nil
}

// SpaceUid 组装空间 UID，格式为 `spaceType__spaceId`
func (s *Space) SpaceUid() string {
	return fmt.Sprintf("%s__%s", s.SpaceTypeId, s.SpaceId)
}
