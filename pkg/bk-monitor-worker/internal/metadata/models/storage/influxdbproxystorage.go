// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

//go:generate goqueryset -in influxdbproxystorage.go -out qs_influxdbproxystorage.go

// InfluxdbProxyStorage influxdb proxy storage model
// gen:qs
type InfluxdbProxyStorage struct {
	ID                  uint   `gorm:"id;primary_key" json:"id"`
	ProxyClusterId      uint   `gorm:"proxy_cluster_id" json:"proxy_cluster_id"`
	InstanceClusterName string `gorm:"size:128" json:"instance_cluster_name"`
	ServiceName         string `gorm:"size:64" json:"service_name"`
	IsDefault           bool   `gorm:"default:false" json:"is_default"`
}

// TableName 用于设置表的别名
func (InfluxdbProxyStorage) TableName() string {
	return "metadata_influxdbproxystorage"
}
