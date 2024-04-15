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
	"github.com/jinzhu/gorm"
)

//go:generate goqueryset -in redisstorage.go -out qs_redisstorage_gen.go

// RedisStorage redis storage model
// gen:qs
type RedisStorage struct {
	TableID          string `json:"table_id" gorm:"primary_key;size:128"`
	Command          string `json:"command" gorm:"size:32"`
	Key              string `json:"key" gorm:"size:256"`
	DB               uint   `json:"db" gorm:"column:db"`
	StorageClusterID uint   `json:"storage_cluster_id" gorm:"storage_cluster_id"`
	IsSentinel       bool   `json:"is_sentinel" gorm:"column:is_sentinel"`
	MasterName       string `json:"master_name" gorm:"size:128"`
}

// TableName 用于设置表的别名
func (RedisStorage) TableName() string {
	return "metadata_redisstorage"
}

// BeforeCreate 配置默认字段
func (r *RedisStorage) BeforeCreate(tx *gorm.DB) error {
	if r.Command == "" {
		r.Command = "PUBLISH"
	}
	return nil
}
