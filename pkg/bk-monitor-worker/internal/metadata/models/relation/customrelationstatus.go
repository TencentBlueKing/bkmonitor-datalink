// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

import (
	"time"
)

//go:generate goqueryset -in customrelationstatus.go -out qs_customrelationstatus_gen.go

// CustomRelationStatus 自定义资源关联状态
type CustomRelationStatus struct {
	ID           int       `gorm:"column:id;type:int;primaryKey;autoIncrement"`
	Creator      string    `gorm:"column:creator;type:varchar(64);not null"`
	CreateTime   time.Time `gorm:"column:create_time;type:datetime(6);not null"`
	Updater      string    `gorm:"column:updater;type:varchar(64);not null"`
	UpdateTime   time.Time `gorm:"column:update_time;type:datetime(6);not null"`
	UID          string    `gorm:"column:uid;type:char(32);not null;uniqueIndex"`
	Generation   int64     `gorm:"column:generation;type:bigint;not null"`
	Namespace    string    `gorm:"column:namespace;type:varchar(128);not null;index"`
	Name         string    `gorm:"column:name;type:varchar(128);not null"`
	Labels       string    `gorm:"column:labels;type:json;not null"`
	FromResource string    `gorm:"column:from_resource;type:varchar(128);not null"`
	ToResource   string    `gorm:"column:to_resource;type:varchar(128);not null"`
}

// TableName 指定数据库表名
func (CustomRelationStatus) TableName() string {
	return "metadata_customrelationstatus"
}
