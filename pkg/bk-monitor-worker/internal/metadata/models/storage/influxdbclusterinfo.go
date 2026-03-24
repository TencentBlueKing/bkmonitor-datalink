// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

//go:generate goqueryset -in influxdbclusterinfo.go -out qs_influxdbclusterinfo_gen.go

// InfluxdbClusterInfo influxdb cluster info model
// gen:qs
type InfluxdbClusterInfo struct {
	HostName     string `gorm:"size:128" json:"host_name"`
	ClusterName  string `gorm:"size:128" json:"cluster_name"`
	HostReadable bool   `gorm:"column:host_readable" json:"host_readable"`
}

// TableName 用于设置表的别名
func (InfluxdbClusterInfo) TableName() string {
	return "metadata_influxdbclusterinfo"
}
