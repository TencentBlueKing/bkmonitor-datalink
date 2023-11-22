// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"time"

	"github.com/jinzhu/gorm"
)

//go:generate goqueryset -in essnapshot.go -out qs_essnapshot_gen.go

// EsSnapshot es snapshot model
// gen:qs
type EsSnapshot struct {
	TableID                      string    `json:"table_id" gorm:"index;size:128"`
	TargetSnapshotRepositoryName string    `json:"target_snapshot_repository_name" gorm:"index;size:128"`
	SnapshotDays                 int       `json:"snapshot_days" gorm:"default:0"`
	CreateTime                   time.Time `json:"create_time"`
	Creator                      string    `gorm:"size:32;default:system" json:"creator"`
	LastModifyTime               time.Time `gorm:"last_modify_time" json:"last_modify_time"`
	LastModifyUser               string    `gorm:"size:32" json:"last_modify_user"`
}

// TableName 用于设置表的别名
func (EsSnapshot) TableName() string {
	return "metadata_essnapshot"
}

// BeforeCreate 新建前时间字段设置为当前时间
func (e *EsSnapshot) BeforeCreate(tx *gorm.DB) error {
	e.LastModifyTime = time.Now()
	e.CreateTime = time.Now()
	return nil
}
