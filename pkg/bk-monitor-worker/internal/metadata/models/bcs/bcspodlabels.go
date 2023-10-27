// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bcs

//go:generate goqueryset -in bcspodlabels.go -out qs_bcspodlabels.go

// BCSPodLabels BCS Pod and labels relation model
// gen:qs
type BCSPodLabels struct {
	ID        uint   `gorm:"primary_key" json:"id"`
	Resource  uint   `gorm:"column:resource" json:"resource"` // BCSPod id
	Label     uint   `gorm:"column:label" json:"label"`       // BCSLabel id
	ClusterID string `gorm:"size:128;index" json:"cluster_id"`
}

// TableName: 用于设置表的别名
func (BCSPodLabels) TableName() string {
	return "bkmonitor_bcspodlabels"
}
