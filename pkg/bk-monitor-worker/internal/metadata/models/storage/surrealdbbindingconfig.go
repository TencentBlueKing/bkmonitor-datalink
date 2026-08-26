// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

//go:generate goqueryset -in surrealdbbindingconfig.go -out qs_surrealdbbindingconfig_gen.go

// SurrealDBBindingConfig SurrealDB 图结果表绑定配置。
// gen:qs
type SurrealDBBindingConfig struct {
	ID                    uint   `json:"id" gorm:"primary_key"`
	BkTenantID            string `json:"bk_tenant_id" gorm:"size:256;default:system"`
	TableID               string `json:"table_id" gorm:"size:255"`
	Namespace             string `json:"namespace" gorm:"size:128"`
	BkbaseResultTableName string `json:"bkbase_result_table_name" gorm:"size:255"`
}

func (SurrealDBBindingConfig) TableName() string {
	return "metadata_surrealdbbindingconfig"
}
