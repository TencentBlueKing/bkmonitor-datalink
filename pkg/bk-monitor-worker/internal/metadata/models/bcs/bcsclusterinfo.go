// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bcs

import (
	"time"

	"github.com/jinzhu/gorm"
)

//go:generate goqueryset -in bcsclusterinfo.go -out qs_bcsclusterinfo_gen.go

var DefaultServiceMonitorDimensionTerm = []string{"bk_monitor_name", "bk_monitor_namespace/bk_monitor_name"}

// BCSClusterInfo BCS cluster info model
// gen:qs
type BCSClusterInfo struct {
	ID                 uint      `gorm:"primary_key" json:"id"`
	ClusterID          string    `gorm:"size:128;index" json:"cluster_id"`
	BCSApiClusterId    string    `gorm:"column:bcs_api_cluster_id;index" json:"bcs_api_cluster_id"`
	BkBizId            int       `gorm:"column:bk_biz_id" json:"bk_biz_id"`
	BkCloudId          *int      `gorm:"column:bk_cloud_id" json:"BkCloudId"`
	ProjectId          string    `gorm:"size:128" json:"project_id"`
	Status             string    `gorm:"size:50;default:running" json:"status"`
	DomainName         string    `gorm:"size:512" json:"domain_name"`
	Port               uint      `json:"port"`
	ServerAddressPath  string    `gorm:"size:512" json:"server_address_path"`
	ApiKeyType         string    `gorm:"size:128;default:authorization" json:"api_key_type"`
	ApiKeyContent      string    `gorm:"size:128" json:"api_key_content"`
	ApiKeyPrefix       string    `gorm:"size:128;default:Bearer" json:"api_key_prefix"`
	IsSkipSslVerify    bool      `gorm:"default:true" json:"is_skip_ssl_verify"`
	CertContent        *string   `json:"cert_content"`
	K8sMetricDataID    uint      `gorm:"column:K8sMetricDataID" json:"K8sMetricDataID"`
	CustomMetricDataID uint      `gorm:"column:CustomMetricDataID" json:"CustomMetricDataID"`
	K8sEventDataID     uint      `gorm:"column:K8sEventDataID" json:"K8sEventDataID"`
	CustomEventDataID  uint      `gorm:"column:CustomEventDataID" json:"CustomEventDataID"`
	SystemLogDataID    uint      `gorm:"column:SystemLogDataID" json:"SystemLogDataID"`
	CustomLogDataID    uint      `gorm:"column:CustomLogDataID" json:"CustomLogDataID"`
	BkEnv              *string   `gorm:"size:32;default: " json:"bk_env"`
	Creator            string    `json:"creator" gorm:"size:32"`
	CreateTime         time.Time `json:"create_time"`
	LastModifyTime     time.Time `gorm:"last_modify_time" json:"last_modify_time"`
	LastModifyUser     string    `gorm:"size:32" json:"last_modify_user"`
}

// TableName: 用于设置表的别名
func (BCSClusterInfo) TableName() string {
	return "metadata_bcsclusterinfo"
}

// BeforeCreate 新建前时间字段设置为当前时间
func (r *BCSClusterInfo) BeforeCreate(tx *gorm.DB) error {
	r.CreateTime = time.Now()
	r.LastModifyTime = time.Now()
	return nil
}
