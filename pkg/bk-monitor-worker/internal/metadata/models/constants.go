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
	// ResultTableFieldTagGroup group
	ResultTableFieldTagGroup = "group"

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
	OptionSegmentedQueryEnable        = "segmented_query_enable"
)

// MeasurementType
const (
	MeasurementTypeBkTraditional          = "bk_traditional_measurement"
	MeasurementTypeBkSplit                = "bk_split_measurement"
	MeasurementTypeBkExporter             = "bk_exporter"
	MeasurementTypeBkStandardV2TimeSeries = "bk_standard_v2_time_series"
)

// ETLConfigType
const (
	// 多指标单表(system)
	ETLConfigTypeBkSystemBasereport     = "bk_system_basereport"
	ETLConfigTypeBkUptimecheckHeartbeat = "bk_uptimecheck_heartbeat"
	ETLConfigTypeBkUptimecheckHttp      = "bk_uptimecheck_http"
	ETLConfigTypeBkUptimecheckTcp       = "bk_uptimecheck_tcp"
	ETLConfigTypeBkUptimecheckUdp       = "bk_uptimecheck_udp"
	ETLConfigTypeBkSystemProcPort       = "bk_system_proc_port"
	ETLConfigTypeBkSystemProc           = "bk_system_proc"
	// 自定义多指标单表
	ETLConfigTypeBkStandardV2TimeSeries = "bk_standard_v2_time_series"
	// 固定指标单表(metric_name)
	ETLConfigTypeBkExporter = "bk_exporter"
	ETLConfigTypeBkStandard = "bk_standard"
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
	StorageTypeDoris    = "doris"
	StorageTypeBkSql    = "bk_sql"
)

const (
	ESQueryMaxSize          = 10000
	ESRemoveTypeVersion     = "7" // 从ES7开始移除_type
	ESFieldTypeObject       = "object"
	ESAliasExpiredDelayDays = 1 // ES别名延迟过期时间
)

const (
	EsSourceTypeLOG    = "log"
	EsSourceTypeBKDATA = "bkdata"
	EsSourceTypeES     = "es"
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
	// 兼容状态
	BcsRawClusterStatusRunning = "RUNNING" // 集群状态RUNNING
	BcsClusterStatusDeleted    = "deleted" // 集群状态deleted
	BcsRawClusterStatusDeleted = "DELETED" // 集群状态RUNNING
	BcsClusterTypeSingle       = "single"  // 独占集群类型
	BcsClusterTypeShared       = "shared"  // 共享集群类型
)

// Label
const (
	LabelTypeSource      = "source_label"
	LabelTypeResultTable = "result_table_label"
	LabelTypeType        = "type_label"
)

const (
	TSGroupDefaultMeasurement = "__default__"
	DorisMeasurement          = "doris"
)

// Datasource
const (
	MinDataId = 1500000 // DATA_ID最小值
	MaxDataId = 2097151 // DATA_ID最大值
)

// DataSourceOption
const (
	OptionTimestampUnit        = "timestamp_precision"
	OptionIsSplitMeasurement   = "is_split_measurement"
	OptionDisableMetricCutter  = "disable_metric_cutter"
	OptionEnableFieldBlackList = "enable_field_black_list"
	OptionFieldWhitelist       = "metric_field_whitelist"
)

// root consul path template
const (
	DataSourceConsulPathTemplate          = "%s/metadata/v1"                           // DataSource的consul根路径
	InfluxdbClusterInfoConsulPathTemplate = "%s%s/metadata/influxdb_info/cluster_info" // InfluxdbClusterInfo的consul根路径
	InfluxdbStorageConsulPathTemplate     = "%s%s/metadata/influxdb_info/router"       // InfluxdbStorage router的consul根路径
	InfluxdbHostInfoConsulPathTemplate    = "%s%s/metadata/influxdb_info/host_info"    // InfluxdbHostInfo的consul根路径
	InfluxdbTagInfoConsulPathTemplate     = "%s%s/metadata/influxdb_info/tag_info"     // InfluxdbTagInfo的consul根路径
	InfluxdbInfoVersionConsulPathTemplate = "%s%s/metadata/influxdb_info/version/"     // InfluxdbInfoVersion的consul路径
)

const RecommendedBkCollectorVersion = "0.16.1061" // 推荐的bkcollector版本

// subscription config
const (
	BkMonitorProxyListenPort = 10205      // bk_monitor_proxy 自定义上报服务监听的端口
	MaxDataIdThroughPut      = 1000       // 单个dataid最大的上报频率(条/min)
	MaxFutureTimeOffset      = 3600       // 支持的最大未来时间，超过这个偏移值，则丢弃
	MaxReqThroughPut         = 4000       // 最大的请求数
	MaxReqLength             = 500 * 1024 // 最大请求Body大小，500KB

)

// space
const (
	SpaceTypeBKCC   = "bkcc"
	SpaceTypeBCS    = "bcs"
	SpaceTypeBKCI   = "bkci"
	SpaceTypeBKSAAS = "bksaas"
	SpaceTypeAll    = "all"

	Bkci1001TableIdPrefix       = "devx_system." // 1001 跨空间类型允许 bkci 访问的结果表前缀
	P4SystemTableIdPrefixToBkCi = "perforce_system."
	Dbm1001TableIdPrefix        = "dbm_system." // 1001 仅允许访问 dbm 相关结果表的前缀
	SystemTableIdPrefix         = "system."

	QueryVmSpaceUidListKey    = "bkmonitorv3:vm-query:space_uid"
	QueryVmSpaceUidChannelKey = "bkmonitorv3:vm-query"
)

// VM
const (
	VmRetentionTime            = "30d" // vm 数据默认保留时间
	VmDataTypeUserCustom       = "user_custom"
	VmDataTypeBcsClusterK8s    = "bcs_cluster_k8s"
	VmDataTypeBcsClusterCustom = "bcs_cluster_custom"
	CmdbLevelVmrt              = "cmdb_level_vm_rt"
	BindingBcsClusterId        = "binding_bcs_cluster_id"
)

// TimeStampLen
const (
	TimeStampLenSecondLen      = 10 // Unix Time Stamp(seconds)
	TimeStampLenMillisecondLen = 13 // Unix Time Stamp(milliseconds)
	TimeStampLenNanosecondLen  = 19 // Unix Time Stamp(nanosecond)
)

const (
	PingServerDefaultDataReportInterval = 60 // 数据上报周期，单位: 秒
	PingServerDefaultExecTotalNum       = 3  // 单个周期内执行的ping次数
	PingServerDefaultMaxBatchSize       = 30 // 单次最多同时ping的IP数量，默认20，尽可能的单次少一点ip，避免瞬间包量太多，导致网卡直接丢包
	PingServerDefaultPingSize           = 16 // ping的大小  默认16个字节
	PingServerDefaultPingTimeout        = 3  // ping的rtt  默认3秒
)

const (
	DatabusStatusRunning  = "running"
	DatabusStatusStarting = "starting"
)

const SystemUser = "system"

const LogReportMaxQPS = 50000 // Log Report Default QPS

var TimeStampLenValeMap = map[int]string{
	TimeStampLenSecondLen:      "Unix Time Stamp(seconds)",
	TimeStampLenMillisecondLen: "Unix Time Stamp(milliseconds)",
	TimeStampLenNanosecondLen:  "Unix Time Stamp(nanosecond)",
}

var BcsMetricLabelPrefix = map[string]string{
	"*":          "kubernetes",
	"node_":      "kubernetes",
	"container_": "kubernetes",
	"kube_":      "kubernetes",
}

// SpaceDataSourceETLList 数据源 ETL 配置
var SpaceDataSourceETLList = []string{
	ETLConfigTypeBkSystemBasereport,
	ETLConfigTypeBkUptimecheckHeartbeat,
	ETLConfigTypeBkUptimecheckHttp,
	ETLConfigTypeBkUptimecheckTcp,
	ETLConfigTypeBkUptimecheckUdp,
	ETLConfigTypeBkSystemProcPort,
	ETLConfigTypeBkSystemProc,
	ETLConfigTypeBkStandardV2TimeSeries,
	ETLConfigTypeBkExporter,
	ETLConfigTypeBkStandard,
}

// SkipDataIdListForBkcc 枚举 0 业务，但不是 bkcc 类型的数据源ID
var SkipDataIdListForBkcc = []uint{1110000}

// 全空间可以访问的结果表，对应的授权数据
var AllSpaceTableIds = []string{"custom_report_aggate.base", "bkm_statistics.base"}
