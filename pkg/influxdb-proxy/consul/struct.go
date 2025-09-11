// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

// TotalInfo 全部info
type TotalInfo struct {
	Route   map[string]*RouteInfo
	Cluster map[string]*ClusterInfo
	Host    map[string]*HostInfo
}

// RouteInfo cluster映射
type RouteInfo struct {
	Cluster      string   `json:"cluster"`
	PartitionTag []string `json:"partition_tag"`
}

// ClusterInfo 集群信息
type ClusterInfo struct {
	HostList           []string `json:"host_list"`
	UnReadableHostList []string `json:"unreadable_host_list"`
}

// HostInfo 主机信息
type HostInfo struct {
	DomainName string `json:"domain_name"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	// support https and http
	Protocol string `json:"protocol"`
	// 兼容默认值为 false 需要保持开启，所以用反状态
	Disabled        bool    `json:"status,omitempty"`
	BackupRateLimit float64 `json:"backup_rate_limit,omitempty"`
}

// TagInfo tag路由信息
type TagInfo struct {
	// 使用中的读写列表，可读可写
	HostList []string `json:"host_list"`
	// 不可读列表，但可写
	UnreadableHost []string `json:"unreadable_host"`
	// 既不可读也不可写，该列表只用于数据迁移之后移除主机
	DeleteHostList []string `json:"delete_host_list"`
	// ready/changed/merging
	Status string `json:"status"`
	// 迁移数据的最早时间戳
	TransportStartAt int64 `json:"transport_start_at"`
	// 迁移数据目前迁移到的时间戳，相当于进度
	TransportLastAt int64 `json:"transport_last_at"`
	// 迁移数据的目标时间戳，last_at如果到达该时间戳，则判断完成迁移
	TransportFinishAt int64 `json:"transport_finish_at"`
}
