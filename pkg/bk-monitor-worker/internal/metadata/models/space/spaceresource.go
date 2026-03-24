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
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

//go:generate goqueryset -in spaceresource.go -out qs_spaceresource_gen.go

// gen:qs
// Space space resource model
type SpaceResource struct {
	Id              int     `gorm:"primary_key" json:"id"`
	SpaceTypeId     string  `gorm:"size:64" json:"spaceTypeId"`
	SpaceId         string  `gorm:"size:128" json:"space_id"`
	ResourceType    string  `gorm:"size:128" json:"resource_type"`
	ResourceId      *string `gorm:"size:64" json:"resource_id"`
	DimensionValues string  `gorm:"type:text" json:"dimensionValues"`
	models.BaseModel
}

// TableName table alias name
func (SpaceResource) TableName() string {
	return "metadata_spaceresource"
}

// SetDimensionValues 设置 DimensionValues
func (o *SpaceResource) SetDimensionValues(dm []map[string]any) error {
	dmJson, err := jsonx.MarshalString(dm)
	if err != nil {
		return errors.Wrapf(err, "marshal DimensionValues [%#v] failed", dm)
	}
	o.DimensionValues = dmJson
	return nil
}

// GetDimensionValues 获取 DimensionValues 对象
func (o *SpaceResource) GetDimensionValues() ([]map[string]any, error) {
	var dm []map[string]any
	if err := jsonx.UnmarshalString(o.DimensionValues, &dm); err != nil {
		return nil, errors.Wrapf(err, "unmarshal DimensionValues [%s] failed", o.DimensionValues)
	}
	return dm, nil
}
