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

//go:generate goqueryset -in essnapshotindice.go -out qs_essnapshotindice.go

// EsSnapshotIndice es snapshot indice model
// gen:qs
type EsSnapshotIndice struct {
	TableID        string     `json:"table_id" gorm:"index;size:128"`
	SnapshotName   string     `json:"snapshot_name" gorm:"size:150"`
	ClusterID      uint       `json:"cluster_id" gorm:"cluster_id"`
	RepositoryName string     `json:"repository_name" gorm:"size:128"`
	IndexName      string     `json:"index_name" gorm:"size:150"`
	DocCount       int64      `json:"doc_count"`
	StoreSize      int64      `json:"store_size"`
	StartTime      *time.Time `json:"start_time"`
	EndTime        *time.Time `json:"end_time"`
}

// TableName 用于设置表的别名
func (EsSnapshotIndice) TableName() string {
	return "metadata_essnapshotindice"
}
