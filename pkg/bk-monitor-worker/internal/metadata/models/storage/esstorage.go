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

//go:generate goqueryset -in esstorage.go -out qs_esstorage.go

// ESStorage es storage model
// gen:qs
type ESStorage struct {
	TableID           string `json:"table_id" gorm:"index;size:128"`
	DateFormat        string `json:"date_format" gorm:"size:64;default:%Y%m%d%H"`
	SliceSize         uint   `json:"slice_size" gorm:"default:500"`
	SliceGap          uint   `json:"slice_gap" gorm:"default:120"`
	Retention         uint   `json:"retention" gorm:";default:30"`
	WarmPhaseDays     uint   `json:"warm_phase_days" gorm:"default:0"`
	WarmPhaseSettings string `json:"warm_phase_settings" gorm:"type:jsonb"`
	TimeZone          int8   `json:"time_zone" gorm:"default:0"`
	IndexSettings     string `json:"index_settings" gorm:"type:jsonb"`
	MappingSettings   string `json:"mapping_settings" gorm:"type:jsonb"`
	StorageClusterID  uint   `json:"storage_cluster_id" gorm:"autoUpdateTime"`
}

// TableName 用于设置表的别名
func (ESStorage) TableName() string {
	return "metadata_esstorage"
}

// GetESClient 获取ES客户端
func (e ESStorage) GetESClient(ctx context.Context) (*elasticsearch.Elasticsearch, error) {
	dbSession := mysql.GetDBSession()
	qs := NewClusterInfoQuerySet(dbSession.DB).ClusterIDEq(e.StorageClusterID)
	var esClusterInfo ClusterInfo
	if err := qs.One(&esClusterInfo); err != nil {
		logger.Errorf("find es storage record [%v] error, %v", e.StorageClusterID, err)
		return nil, err
	}

	client, err := esClusterInfo.GetESClient(ctx)
	if err != nil {
		logger.Errorf("get es client error, %v", err)
		return nil, err
	}
	return client, nil
}
