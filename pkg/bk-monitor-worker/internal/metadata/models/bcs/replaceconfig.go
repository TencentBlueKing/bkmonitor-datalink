// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bcs

//go:generate goqueryset -in replaceconfig.go -out qs_replaceconfig.go

// ReplaceConfig replace config model
// gen:qs
type ReplaceConfig struct {
	RuleName          string  `gorm:"primary_key;size:128" json:"rule_name"`
	IsCommon          bool    `gorm:"column:is_common" json:"is_common"`
	SourceName        string  `gorm:"size:128" json:"source_name"`
	TargetName        string  `gorm:"size:128" json:"target_name"`
	ReplaceType       string  `gorm:"size:128" json:"replace_type"`
	CustomLevel       *string `gorm:"size:128" json:"custom_level"`
	ClusterId         *string `gorm:"size:128" json:"cluster_id"`
	ResourceName      *string `gorm:"size:128" json:"resource_name"`
	ResourceType      *string `gorm:"size:128" json:"resource_type"`
	ResourceNamespace *string `gorm:"size:128" json:"resource_namespace"`
}

// TableName: 用于设置表的别名
func (ReplaceConfig) TableName() string {
	return "metadata_replaceconfig"
}
