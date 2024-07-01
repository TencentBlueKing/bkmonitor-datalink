// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package recordrule

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"

//go:generate goqueryset -in rules.go -out qs_rules_gen.go

// RecordRule record rule model
// gen:qs
type RecordRule struct {
	Id            int    `gorm:"primary_key" json:"id"`
	SpaceType     string `gorm:"size:64" json:"spaceType"`
	SpaceId       string `gorm:"size:128" json:"space_id"`
	TableId       string `gorm:"size:128" json:"table_id"`
	RecordName    string `gorm:"size:128" json:"record_name"`
	RuleType      string `gorm:"size:32" json:"rule_type"`
	RuleConfig    string `gorm:"type:text" json:"rule_config"`
	BkSqlConfig   string `gorm:"type:text" json:"bk_sql_config"`
	RuleMetrics   string `gorm:"type:text" json:"rule_metrics"`
	SrcVmTableIds string `gorm:"type:text" json:"src_vm_table_ids"`
	VmClusterId   int    `gorm:"type:int" json:"vm_cluster_id"`
	DstVmTableId  string `gorm:"size:64" json:"dst_vm_table_id"`
	Status        string `gorm:"size:32" json:"status"`
	models.BaseModelWithTime
}

// TableName table alias name
func (RecordRule) TableName() string {
	return "metadata_recordrule"
}
