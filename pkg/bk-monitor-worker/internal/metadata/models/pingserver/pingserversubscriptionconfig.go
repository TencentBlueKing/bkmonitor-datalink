// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pingserver

import "github.com/jinzhu/gorm"

//go:generate goqueryset -in pingserversubscriptionconfig.go -out qs_pingserversubscriptionconfig_gen.go

// PingServerSubscriptionConfig Ping Server 订阅配置 model
// gen:qs
type PingServerSubscriptionConfig struct {
	SubscriptionId int    `gorm:"primary_key" json:"subscription_id"`
	BkCloudId      int    `gorm:"column:bk_cloud_id" json:"bk_cloud_id"`
	IP             string `gorm:"size:32" json:"ip"`
	BkHostId       *int   `gorm:"column:bk_host_id" json:"bk_host_id"`
	Config         string `gorm:"type:json" json:"config"`
	PluginName     string `gorm:"size:32" json:"pluginName"`
}

// TableName table alias name
func (PingServerSubscriptionConfig) TableName() string {
	return "metadata_pingserversubscriptionconfig"
}

// TableName table alias name
func (p PingServerSubscriptionConfig) BeforeCreate(tx *gorm.DB) error {
	if p.PluginName == "" {
		p.PluginName = "bkmonitorproxy"
	}
	return nil
}
