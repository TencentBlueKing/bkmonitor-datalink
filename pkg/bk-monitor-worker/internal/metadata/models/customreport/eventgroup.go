// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package customreport

//go:generate goqueryset -in eventgroup.go -out qs_eventgroup_gen.go

// EventGroup event group model
// gen:qs
type EventGroup struct {
	CustomGroupBase
	EventGroupID   uint   `json:"event_group_id" gorm:"unique"`
	EventGroupName string `json:"event_group_name" gorm:"size:255"`
}

// TableName 用于设置表的别名
func (eg EventGroup) TableName() string {
	return "metadata_eventgroup"
}
