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
)

//go:generate goqueryset -in storageclusterrecord.go -out qs_storageclusterrecord_gen.go

// ClusterRecord represents the history of collected storage records.
// gen:qs
type ClusterRecord struct {
	// ID 在 enable_time 相同时作为 DESC 排序的稳定 tie-breaker。
	ID uint `json:"id" gorm:"column:id;primary_key"`

	// TableID is the name of the result table.
	TableID string `json:"table_id" gorm:"size:128;index;comment:'采集项结果表名'"`

	// BkTenantID 限定历史分段所属租户，避免同名结果表串用记录。
	BkTenantID string `json:"bk_tenant_id" gorm:"size:256;default:system;comment:'租户ID'"`

	// ClusterID is the ID of the storage cluster.
	ClusterID int64 `json:"cluster_id" gorm:"index;comment:'存储集群ID'"`

	// IsDeleted indicates whether the record is deleted or stopped.
	IsDeleted bool `json:"is_deleted" gorm:"comment:'是否删除/停用'"`

	// IsCurrent indicates whether the record is the current latest storage cluster.
	IsCurrent bool `json:"is_current" gorm:"default:false;comment:'是否是当前最新存储集群'"`

	// Creator is the name of the person who created the record.
	Creator string `json:"creator" gorm:"size:128;comment:'创建者'"`

	// CreateTime is the time when the record was created.
	CreateTime time.Time `json:"create_time" gorm:"autoCreateTime;comment:'创建时间'"`

	// EnableTime is the time when data writing starts; nil is treated as Unix 0 by route composition.
	EnableTime *time.Time `json:"enable_time" gorm:"comment:'启用时间'"`

	// DisableTime is the time when data writing stops.
	DisableTime *time.Time `json:"disable_time" gorm:"comment:'停用时间'"`

	// DeleteTime is the time when the index cleanup is completed.
	DeleteTime *time.Time `json:"delete_time" gorm:"comment:'删除时间'"`

	// Unique constraint: (table_id, cluster_id, enable_time)
	// This ensures uniqueness for a given table, cluster, and time combination.
	_ struct{} `gorm:"uniqueIndex:idx_table_cluster_enable,priority:1"`
}

// TableName 用于设置表的别名
func (ClusterRecord) TableName() string {
	return "metadata_storageclusterrecord"
}
