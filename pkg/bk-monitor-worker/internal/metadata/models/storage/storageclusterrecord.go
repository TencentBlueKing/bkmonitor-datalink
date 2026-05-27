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
	"github.com/samber/lo"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
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
func ComposeTableIDStorageClusterRecords(db *gorm.DB, tableID string, currentTableID ...string) ([]map[string]any, error) {
	logger.Infof("compose_table_id_storage_cluster_records: try to get storage cluster records for table_id->[%s]", tableID)
	routeTableID := tableID
	if len(currentTableID) > 0 && currentTableID[0] != "" {
		routeTableID = currentTableID[0]
	}

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

	clusterIDList := lo.Uniq(lo.FilterMap(records, func(record ClusterRecord, _ int) (uint, bool) {
		if record.ClusterID > 0 {
			return uint(record.ClusterID), true
		}
		return 0, false
	}))

	clusterInfoMap := make(map[uint]ClusterInfo, len(clusterIDList))
	hasDorisRoute := false
	hasESRoute := false
	if len(clusterIDList) > 0 {
		var clusterInfoList []ClusterInfo
		if err = NewClusterInfoQuerySet(db).
			Select(ClusterInfoDBSchema.ClusterID, ClusterInfoDBSchema.ClusterName, ClusterInfoDBSchema.ClusterType).
			ClusterIDIn(clusterIDList...).
			All(&clusterInfoList); err != nil {
			logger.Errorf("compose_table_id_storage_cluster_records: failed to query cluster info for table_id->[%s], error: %v", tableID, err)
			return nil, err
		}
		for _, clusterInfo := range clusterInfoList {
			clusterInfoMap[clusterInfo.ClusterID] = clusterInfo
			switch clusterInfo.ClusterType {
			case models.StorageTypeDoris:
				hasDorisRoute = true
			case models.StorageTypeES:
				hasESRoute = true
			}
		}
	}

	// Doris 路由不能只写 storage_type=bk_sql，还要补 bkbase_table_id 和 doris measurement，
	// 否则 UQ 会沿用外层 ES detail 的 db/measurement 去拼 BKSQL。
	dorisRoute := map[string]any{}
	if hasDorisRoute && db.HasTable(DorisStorage{}) {
		var dorisStorage DorisStorage
		if err = NewDorisStorageQuerySet(db).
			Select(DorisStorageDBSchema.TableID, DorisStorageDBSchema.BkbaseTableID, DorisStorageDBSchema.OriginTableId, DorisStorageDBSchema.StorageClusterID).
			TableIDEq(routeTableID).
			One(&dorisStorage); err != nil && !gorm.IsRecordNotFoundError(err) {
			logger.Errorf("compose_table_id_storage_cluster_records: failed to query doris storage for table_id->[%s], error: %v", routeTableID, err)
			return nil, err
		}
		if dorisStorage.BkbaseTableID == "" && routeTableID != tableID {
			if err = NewDorisStorageQuerySet(db).
				Select(DorisStorageDBSchema.TableID, DorisStorageDBSchema.BkbaseTableID, DorisStorageDBSchema.OriginTableId, DorisStorageDBSchema.StorageClusterID).
				TableIDEq(tableID).
				One(&dorisStorage); err != nil && !gorm.IsRecordNotFoundError(err) {
				logger.Errorf("compose_table_id_storage_cluster_records: failed to query doris storage for table_id->[%s], error: %v", tableID, err)
				return nil, err
			}
		}
		if dorisStorage.BkbaseTableID == "" && dorisStorage.OriginTableId != "" {
			var originDorisStorage DorisStorage
			if err = NewDorisStorageQuerySet(db).
				Select(DorisStorageDBSchema.TableID, DorisStorageDBSchema.BkbaseTableID, DorisStorageDBSchema.StorageClusterID).
				TableIDEq(dorisStorage.OriginTableId).
				One(&originDorisStorage); err != nil && !gorm.IsRecordNotFoundError(err) {
				logger.Errorf("compose_table_id_storage_cluster_records: failed to query origin doris storage for table_id->[%s], origin_table_id->[%s], error: %v", tableID, dorisStorage.OriginTableId, err)
				return nil, err
			}
			if dorisStorage.BkbaseTableID == "" {
				dorisStorage.BkbaseTableID = originDorisStorage.BkbaseTableID
			}
			if dorisStorage.StorageClusterID == 0 {
				dorisStorage.StorageClusterID = originDorisStorage.StorageClusterID
			}
		}
		if dorisStorage.BkbaseTableID != "" {
			dorisRoute["db"] = dorisStorage.BkbaseTableID
			dorisRoute["measurement"] = models.DorisMeasurement
		}
	}

	// ES 路由同样补齐 index_set 和默认 measurement，支持外层是 Doris 时按时间段回查 ES。
	esRoute := map[string]any{}
	if hasESRoute && db.HasTable(ESStorage{}) {
		var esStorage ESStorage
		if err = NewESStorageQuerySet(db).
			Select(ESStorageDBSchema.TableID, ESStorageDBSchema.IndexSet, ESStorageDBSchema.OriginTableId).
			TableIDEq(routeTableID).
			One(&esStorage); err != nil && !gorm.IsRecordNotFoundError(err) {
			logger.Errorf("compose_table_id_storage_cluster_records: failed to query es storage for table_id->[%s], error: %v", routeTableID, err)
			return nil, err
		}
		if esStorage.IndexSet == "" && routeTableID != tableID {
			if err = NewESStorageQuerySet(db).
				Select(ESStorageDBSchema.TableID, ESStorageDBSchema.IndexSet, ESStorageDBSchema.OriginTableId).
				TableIDEq(tableID).
				One(&esStorage); err != nil && !gorm.IsRecordNotFoundError(err) {
				logger.Errorf("compose_table_id_storage_cluster_records: failed to query es storage for table_id->[%s], error: %v", tableID, err)
				return nil, err
			}
		}
		if esStorage.IndexSet == "" && esStorage.OriginTableId != "" {
			var originESStorage ESStorage
			if err = NewESStorageQuerySet(db).
				Select(ESStorageDBSchema.TableID, ESStorageDBSchema.IndexSet).
				TableIDEq(esStorage.OriginTableId).
				One(&originESStorage); err != nil && !gorm.IsRecordNotFoundError(err) {
				logger.Errorf("compose_table_id_storage_cluster_records: failed to query origin es storage for table_id->[%s], origin_table_id->[%s], error: %v", tableID, esStorage.OriginTableId, err)
				return nil, err
			}
			if esStorage.IndexSet == "" {
				esStorage.IndexSet = originESStorage.IndexSet
			}
		}
		if esStorage.IndexSet != "" {
			esRoute["db"] = esStorage.IndexSet
			esRoute["measurement"] = models.TSGroupDefaultMeasurement
		}
	}

	// 组装结果集
	result := make([]map[string]any, 0)
	for _, record := range records {
		// 判断 enable_time 是否为 nil，转换为 Unix 时间戳
		var enableTimestamp int64
		if record.EnableTime != nil {
			enableTimestamp = record.EnableTime.Unix()
		}

		route := map[string]any{
			"storage_id":  record.ClusterID,
			"enable_time": enableTimestamp,
		}
		if clusterInfo, ok := clusterInfoMap[uint(record.ClusterID)]; ok {
			storageType := clusterInfo.ClusterType
			if storageType == models.StorageTypeDoris {
				// metadata_clusterinfo 中 Doris 的 cluster_type 是 doris，UQ 查询侧使用 bk_sql。
				storageType = models.StorageTypeBkSql
				for k, v := range dorisRoute {
					route[k] = v
				}
			} else if storageType == models.StorageTypeES {
				for k, v := range esRoute {
					route[k] = v
				}
			}
			route["storage_type"] = storageType
		}

		// 追加到结果集合
		result = append(result, route)
	}

	logger.Infof("compose_table_id_storage_cluster_records: get storage cluster records for table_id->[%s] success, records->[%v]", tableID, result)
	return result, nil
}
