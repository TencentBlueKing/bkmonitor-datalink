// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package resulttable

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"

//go:generate goqueryset -in esfieldqueryaliasoption.go -out qs_esfieldalias_gen.go

// ESFieldQueryAliasOption ES字段关联别名配置
// gen:qs
type ESFieldQueryAliasOption struct {
	TableID    string `json:"table_id" gorm:"size:128;comment:结果表名"`
	BkTenantID string `json:"bk_tenant_id" gorm:"size:256;default:system;comment:租户ID"`
	FieldPath  string `json:"field_path" gorm:"size:256;comment:原始字段路径"`
	PathType   string `json:"path_type" gorm:"size:128;default:keyword;comment:路径类型"`
	QueryAlias string `json:"query_alias" gorm:"size:256;comment:查询别名"`
	IsDeleted  bool   `json:"is_deleted" gorm:"comment:是否已删除"`
	models.BaseModel
}

// TableName table alias name
func (ESFieldQueryAliasOption) TableName() string {
	return "metadata_esfieldqueryaliasoption"
}
