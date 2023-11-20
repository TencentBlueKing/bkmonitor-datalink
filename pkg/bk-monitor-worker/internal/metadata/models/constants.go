// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package models

const (
	// ResultTableLabelOther
	ResultTableLabelOther = "others"
	// ResultTableFieldTagMetric 指标字段
	ResultTableFieldTagMetric = "metric"
	// ResultTableFieldTypeFloat float type
	ResultTableFieldTypeFloat = "float"
	// ResultTableFieldTypeString string type
	ResultTableFieldTypeString = "string"
	// ResultTableFieldTagDimension dimension
	ResultTableFieldTagDimension = "dimension"

	// EventTargetDimensionName target维度
	EventTargetDimensionName = "target"
)

// ClusterStorageType
const (
	StorageTypeInfluxdb = "influxdb"
	StorageTypeKafka    = "kafka"
	StorageTypeES       = "elasticsearch"
	StorageTypeVM       = "victoria_metrics"
)

const (
	ESQueryMaxSize          = 10000
	ESRemoveTypeVersion     = "7" // 从ES7开始移除_type
	ESFieldTypeObject       = "object"
	ESAliasExpiredDelayDays = 1 // ES别名延迟过期时间
)

// Influxdb Redis Keys
const (
	InfluxdbKeyPrefix             = "bkmonitorv3:influxdb" // 前缀
	InfluxdbHostInfoKey           = "host_info"            // 集群关联的主机信息
	InfluxdbClusterInfoKey        = "cluster_info"         // 存储的集群信息
	InfluxdbProxyStorageRouterKey = "influxdb_proxy"       // 结果表使用的 proxy 集群和实际存储集群的关联关系
	InfluxdbTagInfoKey            = "tag_info"             // 标签信息，主要是用于数据分片
	QueryVmStorageRouterKey       = "query_vm_router"      // 查询 vm 存储的路由信息
)

// root consul path template
const (
	DataSourceConsulPathTemplate          = "%s/metadata/v1"                         // DataSource的consul根路径
	InfluxdbClusterInfoConsulPathTemplate = "%s/metadata/influxdb_info/cluster_info" // InfluxdbClusterInfo的consul根路径
	InfluxdbStorageConsulPathTemplate     = "%s/metadata/influxdb_info/router"       // InfluxdbStorage router的consul根路径
	InfluxdbHostInfoConsulPathTemplate    = "%s/metadata/influxdb_info/host_info"    // InfluxdbHostInfo的consul根路径
	InfluxdbTagInfoConsulPathTemplate     = "%s/metadata/influxdb_info/tag_info"     // InfluxdbTagInfo的consul根路径
	InfluxdbInfoVersionConsulPathTemplate = "%s/metadata/influxdb_info/version/"     // InfluxdbInfoVersion的consul路径
)
