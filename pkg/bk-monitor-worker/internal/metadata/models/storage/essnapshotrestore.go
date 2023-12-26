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

//go:generate goqueryset -in essnapshotrestore.go -out qs_essnapshotrestore_gen.go

// gen:qs
// EsSnapshotRestore es snapshot restore model
type EsSnapshotRestore struct {
	RestoreID        int       `gorm:"column:restore_id;primaryKey;autoIncrement:true" json:"restore_id"`
	TableID          string    `gorm:"column:table_id" json:"table_id"`
	StartTime        time.Time `gorm:"column:start_time" json:"start_time"`
	EndTime          time.Time `gorm:"column:end_time" json:"end_time"`
	ExpiredTime      time.Time `gorm:"column:expired_time" json:"expired_time"`
	ExpiredDelete    bool      `gorm:"column:expired_delete" json:"expired_delete"`
	Indices          string    `gorm:"column:indices" json:"indices"`
	CompleteDocCount int       `gorm:"column:complete_doc_count" json:"complete_doc_count"`
	TotalDocCount    int       `gorm:"column:total_doc_count" json:"total_doc_count"`
	TotalStoreSize   int       `gorm:"column:total_store_size" json:"total_store_size"`
	Duration         int       `gorm:"column:duration" json:"duration"`
	Creator          string    `gorm:"column:creator" json:"creator"`
	CreateTime       time.Time `gorm:"column:create_time" json:"create_time"`
	LastModifyUser   string    `gorm:"column:last_modify_user" json:"last_modify_user"`
	LastModifyTime   time.Time `gorm:"column:last_modify_time" json:"last_modify_time"`
	IsDeleted        bool      `gorm:"column:is_deleted" json:"is_deleted"`
}

// TableName Essnapshotrestore's table name
func (*EsSnapshotRestore) TableName() string {
	return "metadata_essnapshotrestore"
}

// BeforeCreate 新建前时间字段设置为当前时间
func (e *EsSnapshotRestore) BeforeCreate(tx *gorm.DB) error {
	e.LastModifyTime = time.Now()
	e.CreateTime = time.Now()
	return nil
}

// BeforeUpdate 保存前最后修改时间字段设置为当前时间
func (e *EsSnapshotRestore) BeforeUpdate(tx *gorm.DB) error {
	e.LastModifyTime = time.Now()
	return nil
}
