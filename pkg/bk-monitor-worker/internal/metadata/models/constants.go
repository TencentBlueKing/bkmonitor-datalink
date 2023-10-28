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
	// ResultTableFieldTagDimension dimension
	ResultTableFieldTagDimension = "dimension"
	// ResultTableFieldTagTimestamp timestamp
	ResultTableFieldTagTimestamp = "timestamp"

	// ResultTableFieldTypeInt int type
	ResultTableFieldTypeInt = "int"
	// ResultTableFieldTypeFloat float type
	ResultTableFieldTypeFloat = "float"
	// ResultTableFieldTypeString string type
	ResultTableFieldTypeString = "string"
	// ResultTableFieldTypeObject object type
	ResultTableFieldTypeObject = "object"
	// ResultTableFieldTypeBoolean boolean type
	ResultTableFieldTypeBoolean = "boolean"
	// ResultTableFieldTypeTimestamp timestamp type
	ResultTableFieldTypeTimestamp = "timestamp"

	// EventTargetDimensionName target维度
	EventTargetDimensionName = "target"

	ResultTableSchemaTypeFree    = "free"
	ResultTableSchemaTypeDynamic = "dynamic"
	ResultTableSchemaTypeFixed   = "fixed"
)

// ResultTableFieldOption
const RTFOInfluxdbDisabled = "influxdb_disabled" // influxdb_disabled: influxdb专用，表示字段是否不必写入到influxdb

// ResultTableOption
const (
	OptionCustomReportDimensionValues = "dimension_values"
)

// ClusterStorageType
const (
	StorageTypeInfluxdb = "influxdb"
	StorageTypeKafka    = "kafka"
	StorageTypeES       = "elasticsearch"
	StorageTypeRedis    = "redis"
	StorageTypeBkdata   = "bkdata"
	StorageTypeArgus    = "argus"
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

// bcs cluster
const (
	BcsClusterStatusRunning         = "running"                   // 集群状态running
	BcsClusterStatusDeleted         = "deleted"                   // 集群状态deleted
	BcsDataTypeK8sMetric            = "k8s_metric"                // bcs metric类型数据
	BcsDataTypeK8sEvent             = "k8s_event"                 // bcs event类型数据
	BcsDataTypeCustomMetric         = "custom_metric"             // bcs custom_event类型数据
	BcsResourceGroupName            = "monitoring.bk.tencent.com" // 容器资源组名
	BcsResourceVersion              = "v1beta1"                   // 容器资源版本号
	BcsResourceDataIdResourceKind   = "DataID"                    // data_id注入资源类型
	BcsResourceDataIdResourcePlural = "dataids"                   // data_id注入类型查询名
	BcsMonitorResourceGroupName     = "monitoring.coreos.com"     // monitor资源组名
	BcsMonitorResourceVersion       = "v1"                        // monitor资源版本号
	BcsPodMonitorResourcePlural     = "podmonitors"               // pod monitor注入类型查询名
	BcsServiceMonitorResourcePlural = "servicemonitors"           // service monitor注入类型查询名
	BcsPodMonitorResourceUsage      = "metric"                    // pod monitor用途
	BcsServiceMonitorResourceUsage  = "metric"                    // service monitor用途
)

// Label
const (
	LabelTypeSource      = "source_label"
	LabelTypeResultTable = "result_table_label"
	LabelTypeType        = "type_label"
)

const (
	TSGroupDefaultMeasurement = "__default__"
)

// ReplaceConfig
const (
	ReplaceTypesMetric          = "metric"
	ReplaceTypesDimension       = "dimension"
	ReplaceCustomLevelsCluster  = "cluster"
	ReplaceCustomLevelsResource = "resource"
)

// Datasource
const (
	MinDataId = 1500000 // DATA_ID最小值
	MaxDataId = 2097151 // DATA_ID最大值
)

// DataSourceOption
const (
	OptionTimestampUnit = "timestamp_precision"
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
