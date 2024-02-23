// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package customreport

//go:generate goqueryset -in logsubscriptionconfig.go -out qs_logsubscriptionconfig_gen.go

// LogSubscriptionConfig log subscription config model
// gen:qs
type LogSubscriptionConfig struct {
	ID             uint   `json:"id" gorm:"primary_key"`
	BkBizId        int    `json:"bk_biz_id" gorm:"column:bk_biz_id"`
	SubscriptionId int    `json:"subscription_id" gorm:"column:subscription_id"`
	Config         string `json:"config" gorm:"type:json"`
	LogName        string `json:"log_name" gorm:"size:128"`
}

// TableName 用于设置表的别名
func (LogSubscriptionConfig) TableName() string {
	return "metadata_logsubscriptionconfig"
}
