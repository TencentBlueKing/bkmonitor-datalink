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
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/elasticsearch"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

//go:generate goqueryset -in esstorage.go -out qs_esstorage_gen.go

// ESStorage es storage model
// gen:qs
type ESStorage struct {
	TableID           string                       `json:"table_id" gorm:"primary_key;size:128"`
	DateFormat        string                       `json:"date_format" gorm:"size:64"`
	SliceSize         uint                         `json:"slice_size" gorm:"column:slice_size"`
	SliceGap          int                          `json:"slice_gap" gorm:"column:slice_gap"`
	Retention         int                          `json:"retention" gorm:"column:retention"`
	WarmPhaseDays     int                          `json:"warm_phase_days" gorm:"column:warm_phase_days"`
	WarmPhaseSettings string                       `json:"warm_phase_settings" gorm:"warm_phase_settings"`
	TimeZone          int8                         `json:"time_zone" gorm:"column:time_zone"`
	IndexSettings     string                       `json:"index_settings" gorm:"index_settings"`
	MappingSettings   string                       `json:"mapping_settings" gorm:"mapping_settings"`
	StorageClusterID  uint                         `json:"storage_cluster_id" gorm:"storage_cluster_id"`
	SourceType        string                       `json:"source_type" gorm:"column:source_type"`
	IndexSet          string                       `json:"index_set" gorm:"column:index_set"`
	NeedCreateIndex   bool                         `json:"need_create_index" gorm:"column:need_create_index;default:true"`
	OriginTableId     string                       `json:"origin_table_id" gorm:"column:origin_table_id;size:128"`
	esClient          *elasticsearch.Elasticsearch `gorm:"-"`
}

// TableName 用于设置表的别名
func (ESStorage) TableName() string {
	return "metadata_esstorage"
}

// GetESClient 获取ES客户端
func (e *ESStorage) GetESClient(ctx context.Context) (*elasticsearch.Elasticsearch, error) {
	if e.esClient != nil {
		return e.esClient, nil
	}
	dbSession := mysql.GetDBSession()
	var esClusterInfo ClusterInfo
	if err := NewClusterInfoQuerySet(dbSession.DB).ClusterIDEq(e.StorageClusterID).One(&esClusterInfo); err != nil {
		logger.Errorf("find es storage record [%v] error, %v", e.StorageClusterID, err)
		return nil, err
	}

	client, err := esClusterInfo.GetESClient(ctx)
	if err != nil {
		logger.Errorf("cluster [%v] get es client error, %v", e.StorageClusterID, err)
		return nil, err
	}
	e.esClient = client
	return client, nil
}
