// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package space

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"

//go:generate goqueryset -in spacevminfo.go -out qs_spacevminfo_gen.go

// SpaceVmInfo spacevminfo model
// gen:qs
type SpaceVmInfo struct {
	ID              uint   `gorm:"column:id;primaryKey" json:"id"`
	SpaceType       string `gorm:"column:space_type;not null" json:"space_type"`
	SpaceID         string `gorm:"column:space_id;not null" json:"space_id"`
	VMClusterID     uint   `gorm:"column:vm_cluster_id;not null" json:"vm_cluster_id"`
	VMRetentionTime string `gorm:"column:vm_retention_time" json:"vm_retention_time"`
	Status          string `gorm:"column:status" json:"status"`
	models.BaseModel
}

// TableName table alias name
func (SpaceVmInfo) TableName() string {
	return "metadata_spacevminfo"
}
