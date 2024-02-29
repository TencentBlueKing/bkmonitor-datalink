// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bcs

import (
	"time"
)

// BCSResource kubernetes资源描述
type BCSResource struct {
	Id                 uint      `gorm:"primary_key" json:"id"`
	ClusterID          string    `gorm:"size:128" json:"cluster_id"`
	Namespace          string    `gorm:"size:512" json:"namespace"`
	Name               string    `gorm:"size:128" json:"name"`
	BkDataId           uint      `gorm:"column:bk_data_id;" json:"bk_data_id"`
	IsCustomResource   bool      `gorm:"column:is_custom_resource" json:"is_custom_resource"`
	IsCommonDataId     bool      `gorm:"column:is_common_data_id" json:"is_common_data_id"`
	RecordCreateTime   time.Time `json:"record_create_time"`
	ResourceCreateTime time.Time `json:"resource_create_time"`
}
