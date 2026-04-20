// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkdata

import "time"

//go:generate goqueryset -in bkbaseshortchainresulttable.go -out qs_bkbaseshortchainresulttable_gen.go

// BkBaseShortChainResultTable BKBase短链路结果表映射
// 记录原始 BKBase 结果表（VMRT）与监控平台本地结果表（table_id）之间的映射关系
// 用于：
// 1. 在 composeBkBaseShortChainTableIds 中将短链路表纳入空间路由
// 2. 在 push_bkbase_table_id_detail 中组装结果表详情
// gen:qs
type BkBaseShortChainResultTable struct {
	Id             int       `gorm:"primary_key;AUTO_INCREMENT" json:"id"`
	BkTenantId     string    `gorm:"column:bk_tenant_id;size:256;default:system" json:"bk_tenant_id"`
	TableId        string    `gorm:"column:table_id;size:128;index" json:"table_id"`
	BkbaseRtId     string    `gorm:"column:bkbase_rt_id;size:128;index" json:"bkbase_rt_id"`
	BkBizId        int       `gorm:"column:bk_biz_id;index" json:"bk_biz_id"`
	DataLabel      string    `gorm:"column:data_label;size:128;default:''" json:"data_label"`
	CreateTime     time.Time `gorm:"column:create_time;autoCreateTime" json:"create_time"`
	LastModifyTime time.Time `gorm:"column:last_modify_time;autoUpdateTime" json:"last_modify_time"`
}

// TableName 返回表名
func (BkBaseShortChainResultTable) TableName() string {
	return "metadata_bkbaseshortchainresulttable"
}
