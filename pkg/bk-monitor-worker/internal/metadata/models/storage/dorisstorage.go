// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

//go:generate goqueryset -in dorisstorage.go -out qs_dorisstorage_gen.go

// DorisStorage Doris存储表
// gen:qs
type DorisStorage struct {
	TableID            string `json:"table_id" gorm:"primary_key;size:128;comment:结果表ID"`
	BkTenantID         string `json:"bk_tenant_id" gorm:"size:256;default:system;comment:租户ID"`
	BkbaseTableID      string `json:"bkbase_table_id" gorm:"size:128;comment:bkbase表名"`
	SourceType         string `json:"source_type" gorm:"size:32;default:log;comment:数据源类型"`
	IndexSet           string `json:"index_set" gorm:"type:text;comment:索引集"`
	TableType          string `json:"table_type" gorm:"size:128;default:primary_table;comment:物理表类型"`
	FieldConfigMapping string `json:"field_config_mapping" gorm:"type:text;comment:字段/分词配置"`
	ExpireDays         *int   `json:"expire_days" gorm:"default:30;comment:过期天数"`
	StorageClusterID   uint   `json:"storage_cluster_id" gorm:"comment:存储集群"`
}

// TableName 用于设置表的别名
func (DorisStorage) TableName() string {
	return "metadata_dorisstorage"
}
