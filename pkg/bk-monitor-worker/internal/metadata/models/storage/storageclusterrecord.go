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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

//go:generate goqueryset -in storageclusterrecord.go -out qs_storageclusterrecord_gen.go

// ClusterRecord represents the history of collected storage records.
// gen:qs
type ClusterRecord struct {
	// TableID is the name of the result table.
	TableID string `json:"table_id" gorm:"size:128;index;comment:'采集项结果表名'"`

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

	// EnableTime is the time when data writing starts.
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

// ComposeTableIDStorageClusterRecords 组装指定 table_id 的历史存储集群记录
func ComposeTableIDStorageClusterRecords(db *gorm.DB, tableID string) ([]map[string]any, error) {
	logger.Infof("compose_table_id_storage_cluster_records: try to get storage cluster records for table_id->[%s]", tableID)

	var records []ClusterRecord
	// 查询数据库：过滤 table_id 和 is_deleted，按 create_time 升序排列

	err := NewClusterRecordQuerySet(db).
		TableIDEq(tableID).      // 过滤 table_id
		IsDeletedEq(false).      // 过滤 is_deleted = false
		OrderDescByCreateTime(). // 按 create_time 倒序
		Select("cluster_id", "enable_time", "is_current").
		All(&records)
	if err != nil {
		logger.Errorf("compose_table_id_storage_cluster_records: failed to query records for table_id->[%s], error: %v", tableID, err)
		return nil, err
	}

	// 组装结果集
	result := make([]map[string]any, 0)
	for _, record := range records {
		// 判断 enable_time 是否为 nil，转换为 Unix 时间戳
		var enableTimestamp int64
		if record.EnableTime != nil {
			enableTimestamp = record.EnableTime.Unix()
		}

		// 追加到结果集合
		result = append(result, map[string]any{
			"storage_id":  record.ClusterID,
			"enable_time": enableTimestamp,
		})
	}

	logger.Infof("compose_table_id_storage_cluster_records: get storage cluster records for table_id->[%s] success, records->[%v]", tableID, result)
	return result, nil
}
