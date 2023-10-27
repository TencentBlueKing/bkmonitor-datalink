// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bcs

//go:generate goqueryset -in bcspod.go -out qs_bcspod.go

// BCSPod BCS Pod model
// gen:qs
type BCSPod struct {
	BCSBase
	ID                      uint     `gorm:"primary_key" json:"id"`
	Name                    string   `gorm:"size:128" json:"name"`
	Namespace               string   `gorm:"size:128" json:"namespace"`
	NodeName                string   `gorm:"size:128" json:"node_name"`
	NodeIp                  string   `gorm:"size:64;" json:"node_ip"`
	WorkloadType            string   `gorm:"size:128" json:"workload_type"`
	WorkloadName            string   `gorm:"size:128" json:"workload_name"`
	TotalContainerCount     int      `gorm:"column:total_container_count" json:"total_container_count"`
	ReadyContainerCount     int      `gorm:"column:ready_container_count" json:"ready_container_count"`
	PodIp                   string   `gorm:"size:64;" json:"pod_ip"`
	Images                  string   `gorm:"type:text;" json:"images"`
	Restarts                int      `gorm:"column:restarts" json:"restarts"`
	RequestCpuUsageRatio    *float64 `gorm:"default:0" json:"request_cpu_usage_ratio"`
	LimitCpuUsageRatio      *float64 `gorm:"default:0" json:"limit_cpu_usage_ratio"`
	RequestMemoryUsageRatio *float64 `gorm:"default:0" json:"request_memory_usage_ratio"`
	LimitMemoryUsageRatio   *float64 `gorm:"default:0" json:"limit_memory_usage_ratio"`
}

// TableName: 用于设置表的别名
func (BCSPod) TableName() string {
	return "bkmonitor_bcspod"
}
