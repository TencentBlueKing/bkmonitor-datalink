// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package resulttable

//go:generate goqueryset -in label.go -out qs_label.go

// Label label model
// gen:qs
type Label struct {
	LabelId     string  `gorm:"primary_key;size:128" json:"label_id"`
	LabelName   string  `gorm:"size:128" json:"label_name"`
	LabelType   string  `gorm:"size:128" json:"label_type"`
	IsAdminOnly bool    `gorm:"column:is_admin_only" json:"is_admin_only"`
	ParentLabel *string `gorm:"size:128" json:"parent_label"`
	Level       *uint   `gorm:"column:level" json:"level"`
	Index       *uint   `gorm:"column:index" json:"index"`
}

// TableName table alias name
func (Label) TableName() string {
	return "metadata_label"
}
