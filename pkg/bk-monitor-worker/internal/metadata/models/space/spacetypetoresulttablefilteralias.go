// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package space

import (
	"time"
)

//go:generate goqueryset -in spacetypetoresulttablefilteralias.go -out qs_spacetypetoresulttablefilteralias_gen.go

// SpaceTypeToResultTableFilterAlias space type to result table filter alias model
// gen:qs
type SpaceTypeToResultTableFilterAlias struct {
	Id          int       `gorm:"primary_key" json:"id"`
	SpaceType   string    `gorm:"size:64" json:"space_type"`
	TableId     string    `gorm:"size:128" json:"table_id"`
	FilterAlias string    `gorm:"size:128" json:"filter_alias"`
	Status      bool      `json:"status"`
	CreateTime  time.Time `gorm:"autoCreateTime" json:"create_time"`
}

// TableName table alias name
func (SpaceTypeToResultTableFilterAlias) TableName() string {
	return "metadata_spacetypetoresulttablefilteralias"
}
