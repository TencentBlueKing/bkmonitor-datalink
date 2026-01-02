// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

// ClusterInfo cluster info with map
type ClusterInfo map[string]*Cluster

// Cluster info for influxdb，include host list and unreadable host list
type Cluster struct {
	HostList           []string `json:"host_list"`
	UnreadableHostList []string `json:"unreadable_host_list"`
}

// HostInfo host info with map
type HostInfo map[string]*Host

// Host info for influxdb, include host port and so on...
type Host struct {
	DomainName string `json:"domain_name"`
	Port       int    `json:"port"`
	GrpcPort   int    `json:"grpc_port"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	Protocol   string `json:"protocol"`
	// 兼容默认值为 false 需要保持开启，所以用反状态
	Disabled bool `json:"status,omitempty"`

	BackupRateLimit float64 `json:"backup_rate_limit,omitempty"`
	ReadRateLimit   float64 `json:"read_rate_limit,omitempty"`
}

type HostStatusInfo map[string]*HostStatus

// HostStatus Host info's status for influxdb, include read and last modify time
type HostStatus struct {
	Read           bool  `json:"read"`
	LastModifyTime int64 `json:"lastModifyTime"`
}

// TagInfo tag info with map
type TagInfo map[string]*Tag

// Tag info for influxdb conditions
type Tag struct {
	HostList       []string `json:"host_list"`
	UnreadableHost []string `json:"unreadable_host"`
}

type ProxyInfo map[string]*Proxy

type RetentionPolicy struct {
	IsDefault  bool `json:"is_default"`
	Resolution int  `json:"resolution"`
}

type Proxy struct {
	BKBizID           string                     `json:"bk_biz_id,omitempty"`
	DataID            string                     `json:"data_id,omitempty"`
	MeasurementType   string                     `json:"measurement_type,omitempty"`
	StorageID         string                     `json:"storageID,omitempty"`
	ClusterName       string                     `json:"clusterName"`
	TagsKey           []string                   `json:"tagsKey"`
	Db                string                     `json:"db"`
	Measurement       string                     `json:"measurement"`
	RetentionPolicies map[string]RetentionPolicy `json:"retention_policies,omitempty"`
	VmRt              string                     `json:"vm_rt,omitempty"`
}

type QueryRouterInfo map[string]*QueryRouter

type QueryRouter struct {
	BkBizId            string `json:"bk_biz_id"`
	DataId             string `json:"data_id"`
	MeasurementType    string `json:"measurement_type"`
	VmTableId          string `json:"vm_table_id"`
	BcsClusterId       string `json:"bcs_cluster_id"`
	IsInfluxdbDisabled bool   `json:"is_influxdb_disabled"`
}

type BlackListInfo struct {
	ForbiddenVmRt [][]string `json:"forbidden_vm_rt"`
}
