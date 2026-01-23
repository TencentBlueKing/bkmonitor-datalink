// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// 集群化监听
const (
	EventAdded    EventType = iota // 0
	EventDeleted                   // 1
	EventModified                  // 2
)

const (
	PipelineConfigDimensionGroupName = "group_info"
)

const (
	LogCleanFailedFlag = "__parse_failure"
)

// RT 类型
const (
	// ResultTableSchemaTypeFree :
	ResultTableSchemaTypeFree ResultTableSchemaType = "free"
	// ResultTableSchemaTypeFixed :
	ResultTableSchemaTypeFixed ResultTableSchemaType = "fixed"
)

// PipelineConfig 专用
const (
	// 通用
	// PipelineConfigOptUseSourceTime : 使用本地时间替换数据时间(bool)
	PipelineConfigOptUseSourceTime = "use_source_time"
	// PipelineConfigOptAllowMetricsMissing : 允许指标字段缺失(bool)
	PipelineConfigOptAllowMetricsMissing = "allow_metrics_missing"
	// PipelineConfigOptAllowDimensionsMissing : 允许维度字段缺失(bool)
	PipelineConfigOptAllowDimensionsMissing = "allow_dimensions_missing"
	// PipelineConfigOptTimePrecision : 记录时间精度(string)
	PipelineConfigOptTimePrecision = "time_precision"
	// PipelineConfigOptAlignTimeUnit 时间单位对齐
	PipelineConfigOptAlignTimeUnit = "align_time_unit"
	// PipelineConfigOptEnableDimensionGroup : 开启维度组功能(bool)
	PipelineConfigOptEnableDimensionGroup = "enable_dimension_group"
	// PipelineConfigOptDimensionGroupAlias : 维度组别名(string)
	PipelineConfigOptDimensionGroupAlias = "group_info_alias"
	// PipelineConfigOptTransformEnableFieldAlias : 字段别名映射(bool)
	PipelineConfigOptTransformEnableFieldAlias = "allow_use_alias_name"
	// PipelineConfigOptPayloadEncoding : payload 编码
	PipelineConfigOptPayloadEncoding = "encoding"
	// PipelineConfigOptPayloadEncodingStrict : 严格编码模式(bool)
	PipelineConfigOptPayloadEncodingStrict = "encoding_strict"
	// PipelineConfigOptTransformFileNameToAliasName : 字段别名映射
	PipelineConfigOptTransformFileNameToAliasName = "allow_use_alias_name"
	// ResultTableListConfigOptEnableFillDefault : 默认值
	ResultTableListConfigOptEnableFillDefault = "enable_default_value"

	// PipelineConfigOptTimestampPrecision: 时间精度，仅用于检验输入时间精度与配置是否一致
	// available values: ["s", "ms", "ns"]
	PipelineConfigOptTimestampPrecision        = "timestamp_precision"
	PipelineConfigOptTimestampDefaultPrecision = "ms"

	PipelineConfigOptKafkaInitialOffset = "kafka_initial_offset"

	// 时序类
	// PipelineConfigOptInjectLocalTime :  增加入库时间指标(bool)
	PipelineConfigOptInjectLocalTime = "inject_local_time"
	// PipelineConfigOptDisableMetricCutter : 禁用指标切分(bool)
	PipelineConfigOptDisableMetricCutter = "disable_metric_cutter"
	// PipelineConfigOptAllowDynamicMetricsAsFloat : 如果该指标类型不符合要求,是否可以被drop(bool)
	PipelineConfigOptAllowDynamicMetricsAsFloat = "dynamic_metrics_as_float"
	// PipelineConfigOptMaxQps 允许后端写入的最大的 QPS
	PipelineConfigOptMaxQps = "max_qps"
	// PipelineConfigDropEmptyMetrics 是否丢弃空 metrics
	PipelineConfigDropEmptyMetrics = "drop_empty_metrics"
	// PipelineConfigDisableMetricsReporter 是否关闭 metrics_reporter 特性
	PipelineConfigDisableMetricsReporter = "disable_metrics_reporter"

	// 日志类
	// PipelineConfigOptSeparatorNode : "字段提取节点路径"
	PipelineConfigOptSeparatorNodeSource = "separator_node_source"
	// PipelineConfigOptSeparatorNode : "字段提取节点名称"
	PipelineConfigOptSeparatorNode = "separator_node_name"
	// PipelineConfigOptSeparatorAction : "提取方法"
	PipelineConfigOptSeparatorAction = "separator_node_action"
	// PipelineConfigOptLogSeparator : 日志分隔符清洗专用，指定提取分隔符
	PipelineConfigOptLogSeparator = "separator"
	// PipelineConfigOptLogSeparatedFields : 日志分隔符清洗专用，分隔符对应的变量量列列表
	PipelineConfigOptLogSeparatedFields = "separator_field_list"
	// PipelineConfigOptLogSeparatorRegexp : 日志正则提取清洗专用，提取字段
	PipelineConfigOptLogSeparatorRegexp = "separator_regexp"
	PipelineConfigOptionIsLogData       = "is_log_data"
	// PipelineConfigOptionRetainExtraJson : JSON清洗时, 未定义字段将会归到ext里
	PipelineConfigOptionRetainExtraJson = "retain_extra_json"
	// PipelineConfigOptionRetainContent 数据清洗失败时是否保留原始日志文本
	PipelineConfigOptionRetainContent = "enable_retain_content"
	// PipelineConfigOptionRetainContentKey 清洗失败后日志原始文本应该保存的 key
	PipelineConfigOptionRetainContentKey = "retain_content_key"
	// PipelineConfigOptEnableDimensionCmdbLevel : 开启层级组功能
	PipelineConfigOptEnableDimensionCmdbLevel = "enable_dimension_cmdb_level"
	// ResultTableListConfigOptMetricSplitLevel  : 描述需要拆解的层级内容
	ResultTableListConfigOptMetricSplitLevel = "cmdb_level_config"
	// ResultTableListConfigOptMetricSplitLevel  : 描述是否保证拓扑信息一致性
	ResultTableListConfigOptEnableTopo = "enable_topo_config"
	// ResultTableListConfigOptEnableKeepCmdbLevel  : 描述拆解之后,cmdb是否应该保留
	ResultTableListConfigOptEnableKeepCmdbLevel = "enable_keep_cmdb_level"
	// ResultTableListConfigOptEnableDbmMeta 是否开启 dbm_meta 字段注入
	ResultTableListConfigOptEnableDbmMeta = "enable_dbm_meta"
	// ResultTableListConfigOptEnableDevxMeta 是否开启 devx_meta 字段注入
	ResultTableListConfigOptEnableDevxMeta = "enable_devx_meta"
	// ResultTableListConfigOptEnablePerforceMeta 是否启用 perforce_meta 字段注入
	ResultTableListConfigOptEnablePerforceMeta = "enable_perforce_meta"

	// 事件类
	// PipelineConfigOptFlatBatchKey: 事件类数据需要进行进行插件的
	PipelineConfigOptFlatBatchKey = "flat_batch_key"

	// 自定义时序
	// PipelineConfigOptFlatBatchKey: 事件类数据需要进行进行插件的
	PipelineConfigOptMetricsReportPathKey    = "metrics_report_path"
	PipelineConfigCacheFieldRefreshPeriodKey = "cache_field_refresh_period"

	// PipelineConfigIsLogCluster 是否开启日志聚类
	PipelineConfigOptIsLogCluster = "is_log_cluster"

	// PipelineConfigOptBackendFields 聚类中清洗 backend 需要配置指定的入库字段
	PipelineConfigOptBackendFields = "backend_fields"

	// PipelineConfigOptLogClusterConfig 聚类配置
	PipelineConfigOptLogClusterConfig = "log_cluster_config"
)

// MetaResultTableConfig 专用
const (
	// 通用
	// ResultTableOptSchemaDiscovery : 开启结构自动发现功能(bool)
	ResultTableOptSchemaDiscovery = "enable_schema_discovery"

	// 日志类
	// ResultTableOptUniqueFields : 结果表中唯一索引字段
	ResultTableOptLogUniqueFields = "es_unique_field_list"
	// PipelineConfigOptSeparatorNode : "字段提取节点路径"
	ResultTableOptSeparatorNodeSource = "separator_node_source"
	// ResultTableOptSeparatorNode : "字段提取节点名称"
	ResultTableOptSeparatorNode = "separator_node_name"
	// ResultTableOptSeparatorAction : "提取方法"
	ResultTableOptSeparatorAction = "separator_node_action"
	// ResultTableOptLogSeparator : 日志分隔符清洗专用，指定提取分隔符
	ResultTableOptLogSeparator = "separator"
	// ResultTableOptLogSeparatedFields : 日志分隔符清洗专用，分隔符对应的变量量列列表
	ResultTableOptLogSeparatedFields = "separator_field_list"
	// ResultTableOptLogSeparatorRegexp : 日志正则提取清洗专用，提取字段
	ResultTableOptLogSeparatorRegexp = "separator_regexp"

	ResultTableOptLogSeparatorConfigs = "separator_configs"

	// 事件类
	// 结果是否可以使用新的自定义维度
	ResultTableOptEventAllowNewDimension = "allow_new_dimension"
	// 结果表是否可以使用自定义的事件指标内容
	ResultTableOptEventAllowNewEvent = "allow_new_event"
	// 结果表中的目标配置内容
	ResultTableOptEventTargetDimensionName = "target"
	// 结果表中的各个事件和维度对应关系配置
	// 格式为：{"event": ["dimension"]}
	ResultTableOptEventDimensionList = "event_dimension"
	// 结果表中可以可以存在的事件指标字段
	// 格式为：{"event": ["content"]}
	ResultTableOptEventContentList = "event_content"
	// 数据中必须存在的event_content内容
	// 格式为：["event_content", "bk_count"]
	ResultTableOptEventEventMustHave = "must_have_event"
	// 数据中必须存在的event_content内容
	// 格式为：["target"]
	ResultTableOptEventDimensionMustHave = "must_have_dimension"

	// 自定义时序类
	// 如果为true，则所有指标存储于以自己指标名命名的measurement里,否则存储于rt表配置的路径里
	ResultTableOptIsSplitMeasurement = "is_split_measurement"
	// exporter 类型是否开启黑名单功能
	ResultTableOptEnableBlackList = "enable_field_black_list"

	// ResultTableOptMustIncludeDimensions 指标中必须拥有指定的所有维度 否则将丢弃
	ResultTableOptMustIncludeDimensions = "must_include_dimensions"
)

// MetaFieldConfig 专用
const (
	// 通用
	// MetaFieldOptTimeZone : 时区(int)
	MetaFieldOptTimeZone = "time_zone"
	// MetaFieldOptTimeFormat : 时间格式（string)
	MetaFieldOptTimeFormat = "time_format"
	// MetaFieldOptTimeLayout 时间格式模板
	MetaFieldOptTimeLayout = "time_layout"
	// MetaFieldOptRealPath : "提取的真实路径"
	MetaFieldOptRealPath = "real_path"

	// MetaFieldOptDbmEnabled 是否启动 dbm 慢查询解析
	MetaFieldOptDbmEnabled = "dbm_enabled"
	// MetaFieldOptDbmUrl dbm 解析 URL
	MetaFieldOptDbmUrl = "dbm_url"
	// MetaFieldOptDbmField dbm 解析后写入的新字段
	MetaFieldOptDbmField = "dbm_field"
	// MetaFieldOptDbmRetry dbm 解析 URL 重试次数
	MetaFieldOptDbmRetry = "dbm_retry"

	// 允许通过函数决定缺省值
	MetaFieldOptDefaultFunc   = "default_function"
	MetaFieldOptTimestampUnit = "timestamp_unit"

	// 时序类
	// MetaFieldOptInfluxDisabled : 禁止写入 influxdb
	MetaFieldOptInfluxDisabled = "influxdb_disabled"

	// 日志类
	// MetaFieldOptESType : es 对应类型(string)
	MetaFieldOptESType = "es_type"
	// MetaFieldOptESFormat : es 对应格式(string)
	MetaFieldOptESFormat = "es_format"
	// MataFieldOptEnableOriginString 保留原始字符串格式（map[string]interface{}/[]interface{} -> string）
	MataFieldOptEnableOriginString = "enable_origin_string"
)

// InitPipelineOptions : 初始化通用 pipeline option
func InitPipelineOptions(pipe *PipelineConfig) {
	helper := utils.NewMapHelper(pipe.Option)

	helper.SetDefault(PipelineConfigOptUseSourceTime, true)
	helper.SetDefault(PipelineConfigOptDisableMetricCutter, false)
	helper.SetDefault(PipelineConfigOptAllowMetricsMissing, true)
	helper.SetDefault(PipelineConfigOptAllowDimensionsMissing, true)
	helper.SetDefault(PipelineConfigOptTimePrecision, "")
	helper.SetDefault(PipelineConfigOptAlignTimeUnit, "")
	helper.SetDefault(PipelineConfigOptEnableDimensionGroup, true)
	helper.SetDefault(PipelineConfigOptDimensionGroupAlias, PipelineConfigDimensionGroupName)
	helper.SetDefault(PipelineConfigOptTransformEnableFieldAlias, true)
	helper.SetDefault(PipelineConfigOptPayloadEncoding, "")

	pipe.Option = helper.Data
}

// InitTSPipelineOptions
func InitTSPipelineOptions(pipe *PipelineConfig) {
	InitPipelineOptions(pipe)
	helper := utils.NewMapHelper(pipe.Option)

	helper.SetDefault(PipelineConfigOptInjectLocalTime, true)
	helper.SetDefault(PipelineConfigOptDisableMetricCutter, false)
	helper.SetDefault(PipelineConfigOptAllowDynamicMetricsAsFloat, true)
}

// InitLogPipelineOptions
func InitLogPipelineOptions(pipe *PipelineConfig) {
	InitPipelineOptions(pipe)
	helper := utils.NewMapHelper(pipe.Option)

	helper.SetDefault(PipelineConfigOptLogSeparator, " ")
	helper.SetDefault(PipelineConfigOptLogSeparatedFields, []interface{}(nil))
	helper.SetDefault(PipelineConfigOptLogSeparatorRegexp, nil)
}

// InitResultTableOptions
func InitResultTableOptions(rt *MetaResultTableConfig) {
	helper := utils.NewMapHelper(rt.Option)
	helper.SetDefault(ResultTableOptSchemaDiscovery, false)
	rt.Option = helper.Data
}

// InitTSResultTableOptions
func InitTSResultTableOptions(rt *MetaResultTableConfig) {
	InitResultTableOptions(rt)
}

// InitLogResultTableOptions
func InitLogResultTableOptions(rt *MetaResultTableConfig) {
	InitResultTableOptions(rt)
	helper := utils.NewMapHelper(rt.Option)

	helper.SetDefault(ResultTableOptLogUniqueFields, []interface{}(nil))
}

// InitEventResultTableOption: 初始化自定义事件上报的结果表option内容，及时metadata不进行配置，会使用该默认配置
func InitEventResultTableOption(rt *MetaResultTableConfig) {
	helper := utils.NewMapHelper(rt.Option)

	helper.SetDefault(ResultTableOptEventAllowNewEvent, false)
	helper.SetDefault(ResultTableOptEventContentList, map[string]interface{}{"content": struct{}{}, "count": struct{}{}})
	helper.SetDefault(ResultTableOptEventAllowNewDimension, true)
	helper.SetDefault(ResultTableOptEventDimensionMustHave, []string{"target"})
	helper.SetDefault(ResultTableOptEventEventMustHave, []string{"content"})
	helper.SetDefault(ResultTableOptEventDimensionList, make(map[string][]string))
	// 注入event的默认唯一key列表
	helper.SetDefault(ResultTableOptLogUniqueFields, []interface{}{"event", "target", "dimensions", "event_name", "time"})
}

// InitEventPipelineOption: 初始化pipeline的选项内容
func InitEventPipelineOption(pipe *PipelineConfig) {
	helper := utils.NewMapHelper(pipe.Option)
	helper.SetDefault(PipelineConfigOptTimestampPrecision, PipelineConfigOptTimestampDefaultPrecision)
}

// InitTSV2PipelineOptions
func InitTSV2PipelineOptions(pipe *PipelineConfig) {
	InitPipelineOptions(pipe)
	helper := utils.NewMapHelper(pipe.Option)

	helper.SetDefault(PipelineConfigOptMetricsReportPathKey, "")
	helper.SetDefault(PipelineConfigOptFlatBatchKey, "data")
	helper.SetDefault(PipelineConfigCacheFieldRefreshPeriodKey, "2h")
}

// InitTSV2ResultTableOptions
func InitTSV2ResultTableOptions(rt *MetaResultTableConfig) {
	InitResultTableOptions(rt)
}

// InitFTAPipelineOptions
func InitFTAPipelineOptions(pipe *PipelineConfig) {
	InitPipelineOptions(pipe)
	helper := utils.NewMapHelper(pipe.Option)
	helper.SetDefault(PipelineConfigOptFlatBatchKey, "data")
}

// InitFTAResultTableOptions
func InitFTAResultTableOptions(rt *MetaResultTableConfig) {
	InitResultTableOptions(rt)
}
