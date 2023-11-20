// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

//go:generate goqueryset -in kafkastorage.go -out qs_kafkastorage.go

// KafkaStorage kafka storage model
// gen:qs
type KafkaStorage struct {
	TableID          string `json:"table_id" gorm:"primary_key;size:128"`
	Topic            string `json:"topic" gorm:"size:256"`
	Partition        uint   `json:"partition" gorm:"default:1"`
	StorageClusterID uint   `json:"storage_cluster_id" gorm:"storage_cluster_id"`
	Retention        int64  `json:"retention" gorm:"default=1800000"`
}

// TableName 用于设置表的别名
func (KafkaStorage) TableName() string {
	return "metadata_kafkastorage"
}
