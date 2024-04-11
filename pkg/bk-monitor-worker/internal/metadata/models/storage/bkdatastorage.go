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

//go:generate goqueryset -in bkdatastorage.go -out qs_bkdatastorage_gen.go

// BkDataStorage bk data storage model
// gen:qs
type BkDataStorage struct {
	TableID             string `gorm:"column:table_id;primary_key" json:"table_id"`
	RawDataID           int    `gorm:"column:raw_data_id" json:"raw_data_id"`
	EtlJSONConfig       string `gorm:"column:etl_json_config;type:text" json:"etl_json_config"`
	BkDataResultTableID string `gorm:"column:bk_data_result_table_id" json:"bk_data_result_table_id"`
}

// TableName 用于设置表的别名
func (BkDataStorage) TableName() string {
	return "metadata_bkdatastorage"
}

// BeforeCreate 设置默认值
func (b *BkDataStorage) BeforeCreate(tx *gorm.DB) error {
	if b.RawDataID == 0 {
		b.RawDataID = -1
	}
	return nil
}
