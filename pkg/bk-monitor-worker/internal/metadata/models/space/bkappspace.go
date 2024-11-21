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

//go:generate goqueryset -in bkappspace.go -out qs_bkappspace_gen.go

// BkAppSpace bkappspace model
// gen:qs
type BkAppSpace struct {
	BkAppCode string `gorm:"column:bk_app_code;not null;uniqueIndex:idx_app_space,priority:1;"` // 定义字段类型和索引
	SpaceUID  string `gorm:"column:space_uid;not null;uniqueIndex:idx_app_space,priority:2;"`   // 定义字段类型和索引
	Enable    uint8  `gorm:"column:enable;default:1;"`

	models.BaseModel
}

// TableName table alias name
func (BkAppSpace) TableName() string {
	return "metadata_bkappspace"
}
