// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package resulttable

import (
	"time"

	"github.com/jinzhu/gorm"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

//go:generate goqueryset -in resulttablefieldoption.go -out qs_rtfieldoption_gen.go

// ResultTableFieldOption: result table field option model
// gen:qs
type ResultTableFieldOption struct {
	models.OptionBase
	BkTenantId string `gorm:"column:bk_tenant_id;size:256" json:"bk_tenant_id"`
	TableID    string `json:"table_id" gorm:"size:128;unique"`
	FieldName  string `json:"field_name" gorm:"size:255"`
	Name       string `json:"name" gorm:"size:128"`
}

// TableName table alias name
func (ResultTableFieldOption) TableName() string {
	return "metadata_resulttablefieldoption"
}

func (r *ResultTableFieldOption) BeforeCreate(tx *gorm.DB) error {
	r.CreateTime = time.Now()
	return nil
}

func (r *ResultTableFieldOption) InterfaceValue() (any, error) {
	var value any
	switch r.ValueType {
	case "string":
		value = r.Value
		return value, nil
	case "bool":
		value = r.Value == "true"
		return value, nil
	default:
		err := jsonx.UnmarshalString(r.Value, &value)
		if err != nil {
			return nil, err
		}
		return value, nil
	}
}
