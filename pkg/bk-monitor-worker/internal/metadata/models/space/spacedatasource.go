// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package space

//go:generate goqueryset -in spacedatasource.go -out qs_spacedatasource.go

// SpaceDataSource space data source model
// gen:qs
type SpaceDataSource struct {
	Id                int    `gorm:"primary_key" json:"id"`
	SpaceTypeId       string `gorm:"size:64" json:"spaceTypeId"`
	SpaceId           string `gorm:"size:128" json:"space_id"`
	BkDataId          uint   `gorm:"column:bk_data_id" json:"bk_data_id"`
	FromAuthorization bool   `gorm:"column:from_authorization" json:"from_authorization"`
}

// TableName table alias name
func (SpaceDataSource) TableName() string {
	return "metadata_spacedatasource"
}
